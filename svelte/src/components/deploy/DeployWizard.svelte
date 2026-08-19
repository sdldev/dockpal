<script lang="ts">
  import Button from '../ui/Button.svelte';
  import { api, getToken } from '../../lib/api/client';
  import { addToast, currentPage } from '../../lib/store';
  import type { Template } from '../../lib/types/api';

  interface Props {
    template: Template;
    instanceId: string;
    ondone: () => void;
  }

  let { template, instanceId, ondone }: Props = $props();

  const tabs = ['environment', 'ports', 'network', 'advanced', 'logs'] as const;
  type Tab = (typeof tabs)[number];

  let activeTab = $state<Tab>('environment');
  let serviceName = $state(template.id + '-' + Date.now().toString().slice(-6));
  let env = $state<Record<string, string>>({});
  let ports = $state<Record<string, number>>({});
  let networkMode = $state('bridge');
  let customNetwork = $state('');
  let restartPolicy = $state('unless-stopped');
  let autoRecover = $state(false);
  let domain = $state('');
  let deploying = $state(false);
  let logs = $state<Array<{ time: string; message: string; status: string }>>([]);
  let deployError = $state('');
  let socket: WebSocket | null = null;

  for (const key of template.env_required) env[key] = '';
  for (const p of template.ports) ports[String(p.container_port)] = p.default;

  function switchTab(tab: Tab) {
    activeTab = tab;
  }

  function addLog(message: string, status = 'info') {
    logs = [...logs, { time: new Date().toLocaleTimeString(), message, status }];
  }

  async function deploy() {
    if (!serviceName) {
      deployError = 'Service name is required';
      return;
    }
    deploying = true;
    deployError = '';
    logs = [];
    switchTab('logs');
    addLog('Starting deployment...');

    try {
      const resp = await api.post<{ deploy_id: string }>(
        `/instances/${instanceId}/templates/${template.id}/deploy/stream`,
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

      addLog(`Deploy session created: ${resp.deploy_id}`);
      openLogStream(resp.deploy_id);
    } catch (err) {
      deployError = err instanceof Error ? err.message : 'Deploy failed';
      addLog(deployError, 'error');
      deploying = false;
    }
  }

  function openLogStream(deployId: string) {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const token = getToken();
    const wsUrl = `${proto}//${location.host}/api/v1/instances/${instanceId}/deploy/stream/${deployId}?token=${token}`;

    socket = new WebSocket(wsUrl);
    socket.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data) as { message?: string; status?: string; done?: boolean };
        if (data.message) addLog(data.message, data.status || 'info');
        if (data.done) {
          addLog('Deployment complete', 'done');
          deploying = false;
          addToast(`${template.name} deployed`, 'success');
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
</script>

<div class="space-y-4">
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
        <label for="service-name" class="block text-xs font-medium text-zinc-400 mb-1">Service Name</label>
        <input
          id="service-name"
          type="text"
          bind:value={serviceName}
          class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white font-mono"
        />
      </div>
      {#each template.env_required as key}
        <div>
          <label for="env-{key}" class="block text-xs font-medium text-zinc-400 mb-1 font-mono">{key}</label>
          <input
            id="env-{key}"
            type={key.toLowerCase().includes('password') || key.toLowerCase().includes('secret') ? 'password' : 'text'}
            bind:value={env[key]}
            class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white"
            placeholder="Enter value..."
          />
        </div>
      {:else}
        <p class="text-sm text-zinc-500 italic">No environment variables required.</p>
      {/each}
    </div>
  {/if}

  <!-- Ports tab -->
  {#if activeTab === 'ports'}
    <div class="space-y-3">
      {#each template.ports as port}
        <div class="grid grid-cols-12 gap-3 items-end">
          <div class="col-span-5">
            <label class="block text-xs font-medium text-zinc-400 mb-1">{port.label} (Host)</label>
            <input
              type="number"
              min="1"
              max="65535"
              bind:value={ports[String(port.container_port)]}
              class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white font-mono"
            />
          </div>
          <div class="col-span-2 text-center text-zinc-600 pb-2">→</div>
          <div class="col-span-5">
            <span class="block text-xs font-medium text-zinc-400 mb-1">Container</span>
            <input
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
      Deploy Stack
    </Button>
    <Button
      variant="secondary"
      onclick={() => {
        cleanup();
        currentPage.set('templates');
        ondone();
      }}
    >
      Cancel
    </Button>
  </div>
</div>
