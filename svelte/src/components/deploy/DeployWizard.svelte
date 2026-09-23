<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import Button from '../ui/Button.svelte';
  import { api, getToken } from '../../lib/api/client';
  import { addToast } from '../../lib/store';
  import { navigate } from '../../lib/router';
  import { buildCustomCompose, type CustomEnvRow, type CustomPortRow, type CustomVolumeRow } from '../../lib/compose-builder';
  import type { Template } from '../../lib/types/api';

  interface Props {
    template?: Template | null;
    custom?: boolean;
    instanceId: string;
    ondone: () => void;
  }

  let { template = null, custom = false, instanceId, ondone }: Props = $props();

  const tabs = ['environment', 'ports', 'network', 'advanced', 'logs'] as const;
  type Tab = (typeof tabs)[number];

  let activeTab = $state<Tab>('environment');
  let mode = $state<'template' | 'custom'>(custom ? 'custom' : 'template');

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

  function validateTemplate() {
    for (const key of template?.env_required ?? []) {
      if (!env[key] || !String(env[key]).trim()) {
        deployError = `${key} is required`;
        activeTab = 'environment';
        return false;
      }
    }
    return true;
  }

  function validateCustom() {
    if (!customImage.trim()) {
      deployError = 'Docker image is required';
      activeTab = 'environment';
      return false;
    }
    for (const row of customEnv) {
      if (row.key.trim() && !row.value.trim()) {
        deployError = `Value required for ${row.key}`;
        activeTab = 'environment';
        return false;
      }
    }
    for (const row of customPorts) {
      if (row.host > 0 && (row.host < 1 || row.host > 65535 || row.container < 1 || row.container > 65535)) {
        deployError = 'Ports must be 1-65535';
        activeTab = 'ports';
        return false;
      }
    }
    return true;
  }

  async function deploy() {
    if (!serviceName) {
      deployError = 'App name is required';
      return;
    }
    if (mode === 'template' && !validateTemplate()) return;
    if (mode === 'custom' && !validateCustom()) return;

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

  function openLogStream(deployId: string) {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const token = getToken();
    const wsUrl = `${proto}//${location.host}/api/instances/${instanceId}/deploy/stream/${deployId}?token=${token}`;

    socket = new WebSocket(wsUrl);
    socket.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data) as { message?: string; status?: string; done?: boolean };
        if (data.message) addLog(data.message, data.status || 'info');
        if (data.done) {
          addLog('Deployment complete', 'done');
          deploying = false;
          addToast(`${serviceName} deployed`, 'success');
          socket?.close();
        }
      } catch {
        addLog(String(event.data));
      }
    };
    socket.onclose = () => {
      if (deploying) {
        deploying = false;
        addLog('Log stream closed');
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
          class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white font-mono"
        />
      </div>

      {#if mode === 'custom'}
        <div>
          <label for="custom-image" class="block text-xs font-medium text-zinc-400 mb-1">Docker Image *</label>
          <input
            id="custom-image"
            type="text"
            bind:value={customImage}
            placeholder="nginx:1.27-alpine"
            class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white font-mono"
          />
        </div>

        <div>
          <div class="flex items-center justify-between mb-1">
            <label class="block text-xs font-medium text-zinc-400">Environment Variables</label>
            <button onclick={addEnvRow} class="text-xs text-blue-400 hover:text-blue-300">+ Add</button>
          </div>
          <div class="space-y-2">
            {#each customEnv as row, idx (idx)}
              <div class="grid grid-cols-12 gap-2">
                <input
                  type="text"
                  bind:value={row.key}
                  placeholder="KEY"
                  class="col-span-5 px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-xs text-white font-mono"
                />
                <input
                  type={row.key.toLowerCase().includes('password') || row.key.toLowerCase().includes('secret') ? 'password' : 'text'}
                  bind:value={row.value}
                  placeholder="value"
                  class="col-span-6 px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-xs text-white font-mono"
                />
                <button onclick={() => removeEnvRow(idx)} class="col-span-1 text-zinc-600 hover:text-red-400 text-sm">✕</button>
              </div>
            {/each}
          </div>
        </div>

        <div>
          <div class="flex items-center justify-between mb-1">
            <label class="block text-xs font-medium text-zinc-400">Volumes</label>
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
            <label for="env-{key}" class="block text-xs font-medium text-zinc-400 mb-1 font-mono">{key}</label>
            <input
              bind:value={env[key]}
              class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white"
              id="env-{key}"
              placeholder="Enter value..."
              type={key.toLowerCase().includes('password') || key.toLowerCase().includes('secret') ? 'password' : 'text'}
            />
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
            <label class="block text-xs font-medium text-zinc-400">Port Mappings</label>
            <button onclick={addPortRow} class="text-xs text-blue-400 hover:text-blue-300">+ Add</button>
          </div>
          <div class="space-y-2">
            {#each customPorts as row, idx (idx)}
              <div class="grid grid-cols-12 gap-2 items-center">
                <input
                  type="number"
                  min="1"
                  max="65535"
                  bind:value={row.host}
                  placeholder="Host"
                  class="col-span-3 px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-xs text-white font-mono"
                />
                <span class="col-span-1 text-center text-zinc-600 text-xs">→</span>
                <input
                  type="number"
                  min="1"
                  max="65535"
                  bind:value={row.container}
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
                class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white font-mono"
              />
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
        navigate('templates');
        ondone();
      }}
    >
      Cancel
    </Button>
  </div>
</div>