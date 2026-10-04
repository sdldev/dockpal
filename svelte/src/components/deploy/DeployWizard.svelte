<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import Button from '../ui/Button.svelte';
  import { api } from '../../lib/api/client';
  import { instanceWSURL, wsCredential } from '../../lib/ws';
  import { addToast, currentStackName } from '../../lib/store';
  import { navigate } from '../../lib/router';
  import { buildCustomCompose, type CustomEnvRow, type CustomPortRow, type CustomVolumeRow } from '../../lib/compose-builder';
  import type { Template } from '../../lib/types/api';

  interface Props {
    template?: Template | null;
    custom?: boolean;
    instanceId: string;
    ondone: () => void;
    // Where the Cancel button returns to (App Installer used to live on its
    // own page; it is now the Stacks page's Catalog tab).
    cancelPage?: string;
  }

  let { template = null, custom = false, instanceId, ondone, cancelPage = 'stacks' }: Props = $props();

  const tabs = ['environment', 'ports', 'network', 'advanced', 'logs'] as const;
  type Tab = (typeof tabs)[number];

  let activeTab = $state<Tab>('environment');
  let mode = $state<'template' | 'custom'>('template');
  $effect.pre(() => {
    // Derive initial mode once from the prop; later changes stay manual.
    if (mode === 'template' && custom) mode = 'custom';
  });

  // Shared
  let serviceName = $state('');
  let restartPolicy = $state('unless-stopped');
  let autoRecover = $state(false);
  let domain = $state('');
  let networkMode = $state<'bridge' | 'host' | 'none' | 'custom'>('bridge');
  let customNetwork = $state('');
  let deploying = $state(false);
  let logs = $state<Array<{ time: string; message: string; status: string }>>([]);
  let deployError = $state('');
  let socket: WebSocket | null = null;
  // Per-field validation messages, keyed by field id (serviceName, env-KEY,
  // port-CONTAINER, customImage, cenv-N, cport-N).
  let errors = $state<Record<string, string>>({});

  // Template mode
  let env = $state<Record<string, string>>({});
  let ports = $state<Record<string, number>>({});

  // Custom mode
  let customImage = $state('');
  let customEnv = $state<CustomEnvRow[]>([]);
  let customPorts = $state<CustomPortRow[]>([{ host: 0, container: 0, protocol: 'tcp' }]);
  let customVolumes = $state<CustomVolumeRow[]>([]);

  function initTemplate() {
    if (!template) return;
    serviceName = template.id + '-' + Date.now().toString().slice(-6);
    env = {};
    for (const key of template.env_required) env[key] = '';
    ports = {};
    for (const p of template.ports) ports[String(p.container_port)] = p.default;
  }

  function initCustom() {
    serviceName = 'my-app-' + Date.now().toString().slice(-6);
    customImage = '';
    customEnv = [];
    customPorts = [{ host: 0, container: 0, protocol: 'tcp' }];
    customVolumes = [];
    networkMode = 'bridge';
    customNetwork = '';
  }

  onMount(() => {
    if (mode === 'template') initTemplate();
    else initCustom();
  });

  function switchMode(next: 'template' | 'custom') {
    mode = next;
    deployError = '';
    activeTab = 'environment';
    if (next === 'template') initTemplate();
    else initCustom();
  }

  // Close the deploy stream socket if the wizard unmounts mid-deploy
  onDestroy(() => {
    if (socket) {
      socket.close();
      socket = null;
    }
  });

  function switchTab(tab: Tab) {
    activeTab = tab;
  }

  function addLog(message: string, status = 'info') {
    logs = [...logs, { time: new Date().toLocaleTimeString(), message, status }];
  }

  // Mirrors internal/validator.ValidateContainerName so an invalid name is
  // rejected in the UI before it reaches the backend.
  const SERVICE_NAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9_.\-]*$/;

  function clearError(key: string) {
    if (errors[key]) {
      const next = { ...errors };
      delete next[key];
      errors = next;
    }
  }

  // validate() recomputes every field error and returns whether the form is
  // valid. It also points the user at the first tab holding an error.
  function validate(): boolean {
    const e: Record<string, string> = {};

    const name = serviceName.trim();
    if (!name) {
      e.serviceName = 'App name is required';
    } else if (name.length > 128) {
      e.serviceName = 'App name must be 128 characters or fewer';
    } else if (!SERVICE_NAME_RE.test(name)) {
      e.serviceName = 'App name must start with a letter or digit and can only contain letters, digits, ".", "_", and "-"';
    }

    if (mode === 'template') {
      for (const key of template?.env_required ?? []) {
        if (!env[key] || !String(env[key]).trim()) {
          e[`env-${key}`] = `${key} is required`;
        }
      }
      for (const p of template?.ports ?? []) {
        const hp = ports[String(p.container_port)];
        if (hp !== undefined && (hp < 1 || hp > 65535)) {
          e[`port-${p.container_port}`] = 'Host port must be between 1 and 65535';
        }
      }
    } else {
      if (!customImage.trim()) {
        e.customImage = 'Docker image is required';
      }
      customEnv.forEach((row, idx) => {
        if (row.key.trim() && !row.value.trim()) {
          e[`cenv-${idx}`] = `Value required for ${row.key}`;
        }
      });
      customPorts.forEach((row, idx) => {
        if (row.host > 0 && (row.host < 1 || row.host > 65535 || row.container < 1 || row.container > 65535)) {
          e[`cport-${idx}`] = 'Ports must be between 1 and 65535';
        }
      });
    }

    errors = e;

    if (Object.keys(e).length > 0) {
      const hasEnvError = e.serviceName || Object.keys(e).some((k) => k.startsWith('env-') || k === 'customImage');
      const hasPortError = Object.keys(e).some((k) => k.startsWith('port-') || k.startsWith('cport-'));
      if (hasPortError && !hasEnvError) {
        activeTab = 'ports';
      } else {
        activeTab = 'environment';
      }
    }
    return Object.keys(e).length === 0;
  }

  async function deploy() {
    if (!validate()) {
      deployError = Object.values(errors)[0];
      return;
    }

    deploying = true;
    deployError = '';
    logs = [];
    switchTab('logs');
    addLog('Starting deployment...');

    try {
      let deployId: string;
      if (mode === 'template') {
        const resp = await api.post<{ deploy_id: string }>(
          `/instances/${instanceId}/templates/${template?.id}/deploy/stream`,
          {
            env,
            ports,
            custom_name: serviceName,
            restart_policy: restartPolicy,
            auto_recover: autoRecover,
            domain,
            network_mode: networkMode,
            custom_network: customNetwork
          }
        );
        deployId = resp.deploy_id;
      } else {
        const compose = buildCustomCompose({
          serviceName,
          image: customImage.trim(),
          ports: customPorts.filter((p) => p.host > 0),
          env: customEnv.filter((r) => r.key.trim()),
          volumes: customVolumes.filter((r) => r.host.trim()),
          networkMode,
          customNetwork
        });
        const resp = await api.post<{ deploy_id: string }>(
          `/instances/${instanceId}/deploy/stream`,
          {
            name: serviceName,
            domain: domain || undefined,
            compose,
            restart_policy: restartPolicy,
            auto_start: true
          }
        );
        deployId = resp.deploy_id;
      }

      addLog(`Deploy session created: ${deployId}`);
      openLogStream(deployId);
    } catch (err) {
      deployError = err instanceof Error ? err.message : 'Deploy failed';
      addLog(deployError, 'error');
      deploying = false;
    }
  }

  async function openLogStream(deployId: string) {
    // Single-use 60s ticket instead of the 4h JWT in the URL (audit L1).
    const credential = await wsCredential();
    const wsUrl = instanceWSURL(instanceId, `/deploy/stream/${deployId}`, credential);

    socket = new WebSocket(wsUrl);
    socket.onmessage = (event) => {
      try {
        // The backend streams DeployEvent { step, message, status, time }.
        // Terminal success is { step: "complete", status: "done" }; failures
        // arrive as status: "error" events before the stream closes. There is
        // no `done` boolean field, so match on step/status — not data.done.
        const data = JSON.parse(event.data) as { step?: string; message?: string; status?: string; time?: string };
        if (data.message) addLog(data.message, data.status || 'info');

        if (data.status === 'error') {
          deployError = data.message ?? 'Deployment failed';
          addToast(deployError, 'error');
          deploying = false;
          return;
        }

        if (data.step === 'complete' && data.status === 'done') {
          deploying = false;
          addToast(`${serviceName} deployed`, 'success');
          socket?.close();
          // Auto-close the modal and take the user to the new stack so the
          // outcome is unambiguous (per UX report: the modal used to linger).
          currentStackName.set(serviceName);
          ondone();
          navigate('compose');
        }
      } catch {
        addLog(String(event.data));
      }
    };
    socket.onclose = () => {
      // If the stream closes while still "deploying" and we never saw a
      // terminal event, treat it as a failure rather than silently resetting —
      // otherwise a crashed deploy looks identical to a success-without-toast.
      if (deploying) {
        deploying = false;
        deployError = 'Connection to the deploy stream closed before completion.';
        addLog('Log stream closed before completion', 'error');
        addToast('Deploy stream closed unexpectedly', 'error');
      }
    };
    socket.onerror = () => {
      addLog('Log stream error', 'error');
    };
  }

  function cleanup() {
    socket?.close();
    socket = null;
  }

  function addEnvRow() {
    customEnv = [...customEnv, { key: '', value: '' }];
  }
  function removeEnvRow(index: number) {
    customEnv = customEnv.filter((_, i) => i !== index);
  }
  function addPortRow() {
    customPorts = [...customPorts, { host: 0, container: 0, protocol: 'tcp' }];
  }
  function removePortRow(index: number) {
    customPorts = customPorts.filter((_, i) => i !== index);
  }
  function addVolumeRow() {
    customVolumes = [...customVolumes, { host: '', container: '' }];
  }
  function removeVolumeRow(index: number) {
    customVolumes = customVolumes.filter((_, i) => i !== index);
  }
</script>

<div class="space-y-4">
  <!-- Mode toggle -->
  <div class="flex gap-2">
    <button
      onclick={() => switchMode('template')}
      disabled={!template}
      class="flex-1 px-3 py-2 text-xs font-medium rounded-sm border transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
      class:bg-white={mode === 'template'}
      class:text-zinc-900={mode === 'template'}
      class:bg-zinc-900={mode !== 'template'}
      class:border-zinc-700={mode !== 'template'}
      class:text-zinc-400={mode !== 'template'}
    >
      Template Install
    </button>
    <button
      onclick={() => switchMode('custom')}
      class="flex-1 px-3 py-2 text-xs font-medium rounded-sm border transition-colors"
      class:bg-white={mode === 'custom'}
      class:text-zinc-900={mode === 'custom'}
      class:bg-zinc-900={mode !== 'custom'}
      class:border-zinc-700={mode !== 'custom'}
      class:text-zinc-400={mode !== 'custom'}
    >
      Custom Install
    </button>
  </div>

  <!-- Tabs -->
  <div class="flex gap-1 border-b border-zinc-800 overflow-x-auto">
    {#each tabs as tab}
      <button
        onclick={() => switchTab(tab)}
        class="px-4 py-2.5 text-sm font-medium border-b-2 capitalize whitespace-nowrap transition-colors"
        class:border-blue-500={activeTab === tab}
        class:text-white={activeTab === tab}
        class:border-transparent={activeTab !== tab}
        class:text-zinc-500={activeTab !== tab}
      >
        {tab}
      </button>
    {/each}
  </div>

  <!-- Environment tab -->
  {#if activeTab === 'environment'}
    <div class="space-y-4">
      <div>
        <label for="service-name" class="block text-xs font-medium text-zinc-400 mb-1">App Name</label>
        <input
          id="service-name"
          type="text"
          bind:value={serviceName}
          oninput={() => clearError('serviceName')}
          class="w-full px-3 py-2 bg-zinc-950 border rounded-sm text-sm text-white font-mono"
          class:border-red-500={errors.serviceName}
          class:border-zinc-800={!errors.serviceName}
        />
        {#if errors.serviceName}
          <p class="text-xs text-red-400 mt-1">{errors.serviceName}</p>
        {/if}
      </div>

      {#if mode === 'custom'}
        <div>
          <label for="custom-image" class="block text-xs font-medium text-zinc-400 mb-1">Docker Image *</label>
          <input
            id="custom-image"
            type="text"
            bind:value={customImage}
            oninput={() => clearError('customImage')}
            placeholder="nginx:1.27-alpine"
            class="w-full px-3 py-2 bg-zinc-950 border rounded-sm text-sm text-white font-mono"
            class:border-red-500={errors.customImage}
            class:border-zinc-800={!errors.customImage}
          />
          {#if errors.customImage}
            <p class="text-xs text-red-400 mt-1">{errors.customImage}</p>
          {/if}
        </div>

        <div>
          <div class="flex items-center justify-between mb-1">
            <span id="env-vars-label" class="block text-xs font-medium text-zinc-400">Environment Variables</span>
            <button onclick={addEnvRow} class="text-xs text-blue-400 hover:text-blue-300">+ Add</button>
          </div>
          <div class="space-y-2">
            {#each customEnv as row, idx (idx)}
              <div class="space-y-1">
                <div class="grid grid-cols-12 gap-2">
                  <input
                    type="text"
                    bind:value={row.key}
                    oninput={() => clearError(`cenv-${idx}`)}
                    placeholder="KEY"
                    class="col-span-5 px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-xs text-white font-mono"
                  />
                  <input
                    type={row.key.toLowerCase().includes('password') || row.key.toLowerCase().includes('secret') ? 'password' : 'text'}
                    bind:value={row.value}
                    oninput={() => clearError(`cenv-${idx}`)}
                    placeholder="value"
                    class="col-span-6 px-3 py-2 bg-zinc-950 border rounded-sm text-xs text-white font-mono"
                    class:border-red-500={errors[`cenv-${idx}`]}
                    class:border-zinc-800={!errors[`cenv-${idx}`]}
                  />
                  <button onclick={() => removeEnvRow(idx)} class="col-span-1 text-zinc-600 hover:text-red-400 text-sm">✕</button>
                </div>
                {#if errors[`cenv-${idx}`]}
                  <p class="text-xs text-red-400">{errors[`cenv-${idx}`]}</p>
                {/if}
              </div>
            {/each}
          </div>
        </div>

        <div>
          <div class="flex items-center justify-between mb-1">
            <span class="block text-xs font-medium text-zinc-400">Volumes</span>
            <button onclick={addVolumeRow} class="text-xs text-blue-400 hover:text-blue-300">+ Add</button>
          </div>
          <div class="space-y-2">
            {#each customVolumes as row, idx (idx)}
              <div class="grid grid-cols-12 gap-2">
                <input
                  type="text"
                  bind:value={row.host}
                  placeholder="/srv/my-app"
                  class="col-span-5 px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-xs text-white font-mono"
                />
                <input
                  type="text"
                  bind:value={row.container}
                  placeholder="/app/data"
                  class="col-span-6 px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-xs text-white font-mono"
                />
                <button onclick={() => removeVolumeRow(idx)} class="col-span-1 text-zinc-600 hover:text-red-400 text-sm">✕</button>
              </div>
            {/each}
          </div>
          <p class="text-[11px] text-zinc-600 mt-1">Host path → container path. Use <span class="font-mono">volname:/path</span> for a named volume.</p>
        </div>
      {:else}
        {#each template?.env_required ?? [] as key}
          <div>
            <label for="env-{key}" class="block text-xs font-medium text-zinc-400 mb-1 font-mono">{key} *</label>
            <input
              bind:value={env[key]}
              oninput={() => clearError(`env-${key}`)}
              class="w-full px-3 py-2 bg-zinc-950 border rounded-sm text-sm text-white"
              class:border-red-500={errors[`env-${key}`]}
              class:border-zinc-800={!errors[`env-${key}`]}
              id="env-{key}"
              placeholder="Enter value..."
              type={key.toLowerCase().includes('password') || key.toLowerCase().includes('secret') ? 'password' : 'text'}
            />
            {#if errors[`env-${key}`]}
              <p class="text-xs text-red-400 mt-1">{errors[`env-${key}`]}</p>
            {/if}
          </div>
        {:else}
          <p class="text-sm text-zinc-500 italic">No environment variables required.</p>
        {/each}
      {/if}
    </div>
  {/if}

  <!-- Ports tab -->
  {#if activeTab === 'ports'}
    <div class="space-y-3">
      {#if mode === 'custom'}
        <div>
          <div class="flex items-center justify-between mb-1">
            <span class="block text-xs font-medium text-zinc-400">Port Mappings</span>
            <button onclick={addPortRow} class="text-xs text-blue-400 hover:text-blue-300">+ Add</button>
          </div>
          <div class="space-y-2">
            {#each customPorts as row, idx (idx)}
              <div class="space-y-1">
                <div class="grid grid-cols-12 gap-2 items-center">
                  <input
                    type="number"
                    min="1"
                    max="65535"
                    bind:value={row.host}
                    oninput={() => clearError(`cport-${idx}`)}
                    placeholder="Host"
                    class="col-span-3 px-3 py-2 bg-zinc-950 border rounded-sm text-xs text-white font-mono"
                    class:border-red-500={errors[`cport-${idx}`]}
                    class:border-zinc-800={!errors[`cport-${idx}`]}
                  />
                  <span class="col-span-1 text-center text-zinc-600 text-xs">→</span>
                  <input
                    type="number"
                    min="1"
                    max="65535"
                    bind:value={row.container}
                    oninput={() => clearError(`cport-${idx}`)}
                    placeholder="Container"
                    class="col-span-4 px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-xs text-white font-mono"
                  />
                  <select
                    bind:value={row.protocol}
                    class="col-span-3 px-2 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-xs text-white"
                  >
                    <option value="tcp">tcp</option>
                    <option value="udp">udp</option>
                  </select>
                  <button onclick={() => removePortRow(idx)} class="col-span-1 text-zinc-600 hover:text-red-400 text-sm">✕</button>
                </div>
                {#if errors[`cport-${idx}`]}
                  <p class="text-xs text-red-400">{errors[`cport-${idx}`]}</p>
                {/if}
              </div>
            {/each}
          </div>
        </div>
      {:else}
        {#each template?.ports ?? [] as port}
          <div class="grid grid-cols-12 gap-3 items-end">
            <div class="col-span-5">
              <label for="port-{port.container_port}" class="block text-xs font-medium text-zinc-400 mb-1">{port.label} (Host)</label>
              <input
                id="port-{port.container_port}"
                type="number"
                min="1"
                max="65535"
                bind:value={ports[String(port.container_port)]}
                oninput={() => clearError(`port-${port.container_port}`)}
                class="w-full px-3 py-2 bg-zinc-950 border rounded-sm text-sm text-white font-mono"
                class:border-red-500={errors[`port-${port.container_port}`]}
                class:border-zinc-800={!errors[`port-${port.container_port}`]}
              />
              {#if errors[`port-${port.container_port}`]}
                <p class="text-xs text-red-400 mt-1">{errors[`port-${port.container_port}`]}</p>
              {/if}
            </div>
            <div class="col-span-2 text-center text-zinc-600 pb-2">→</div>
            <div class="col-span-5">
              <label for="port-{port.container_port}-container" class="block text-xs font-medium text-zinc-400 mb-1">Container</label>
              <input
                id="port-{port.container_port}-container"
                type="number"
                disabled
                value={port.container_port}
                class="w-full px-3 py-2 bg-zinc-950/50 border border-zinc-800 rounded-sm text-sm text-zinc-500 font-mono"
              />
            </div>
          </div>
        {:else}
          <p class="text-sm text-zinc-500 italic">No ports to configure.</p>
        {/each}
      {/if}
    </div>
  {/if}

  <!-- Network tab -->
  {#if activeTab === 'network'}
    <div class="space-y-4">
      <div>
        <label for="network-mode" class="block text-xs font-medium text-zinc-400 mb-1">Network Mode</label>
        <select
          id="network-mode"
          bind:value={networkMode}
          class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white"
        >
          <option value="bridge">bridge (default)</option>
          <option value="host">host</option>
          <option value="none">none (isolated)</option>
          <option value="custom">custom network</option>
        </select>
      </div>
      {#if networkMode === 'custom'}
        <div>
          <label for="custom-network" class="block text-xs font-medium text-zinc-400 mb-1">Custom Network Name</label>
          <input
            id="custom-network"
            type="text"
            bind:value={customNetwork}
            placeholder="my-network"
            class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white font-mono"
          />
        </div>
      {/if}
      <div>
        <label for="domain" class="block text-xs font-medium text-zinc-400 mb-1">Domain (Traefik, optional)</label>
        <input
          id="domain"
          type="text"
          bind:value={domain}
          placeholder="app.example.com"
          class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white"
        />
      </div>
    </div>
  {/if}

  <!-- Advanced tab -->
  {#if activeTab === 'advanced'}
    <div class="space-y-4">
      <div>
        <label for="restart-policy" class="block text-xs font-medium text-zinc-400 mb-1">Restart Policy</label>
        <select
          id="restart-policy"
          bind:value={restartPolicy}
          class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white"
        >
          <option value="unless-stopped">unless-stopped (recommended)</option>
          <option value="always">always</option>
          <option value="on-failure">on-failure</option>
          <option value="no">no</option>
        </select>
      </div>
      <label class="flex items-center gap-3 cursor-pointer">
        <input type="checkbox" bind:checked={autoRecover} class="w-4 h-4 rounded bg-zinc-950 border-zinc-700" />
        <span class="text-sm text-white">Auto-recovery</span>
      </label>
    </div>
  {/if}

  <!-- Logs tab -->
  {#if activeTab === 'logs'}
    <div class="bg-black/50 rounded-sm p-4 h-64 overflow-y-auto font-mono text-xs space-y-1 border border-zinc-800/50">
      {#each logs as log, idx (idx)}
        <div class="flex items-start gap-2">
          <span class="text-zinc-600 shrink-0">{log.time}</span>
          <span
            class:text-red-400={log.status === 'error'}
            class:text-emerald-400={log.status === 'done'}
            class:text-blue-400={log.status !== 'error' && log.status !== 'done'}
          >
            {log.message}
          </span>
        </div>
      {:else}
        <div class="text-zinc-700 text-center py-8">Click "Deploy" to start</div>
      {/each}
    </div>
  {/if}

  {#if deployError}
    <div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm">
      <p class="text-sm text-red-400">{deployError}</p>
    </div>
  {/if}

  <!-- Actions -->
  <div class="flex gap-2 pt-2">
    <Button variant="primary" loading={deploying} onclick={deploy}>
      {mode === 'custom' ? 'Install App' : 'Deploy Stack'}
    </Button>
    <Button
      variant="secondary"
      onclick={() => {
        cleanup();
        navigate(cancelPage);
        ondone();
      }}
    >
      Cancel
    </Button>
  </div>
</div>