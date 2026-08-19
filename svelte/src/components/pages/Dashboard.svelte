<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../lib/api/client';
  import { services } from '../../lib/store';
  import type { ContainerInfo, Service } from '../../lib/types/api';

  let containers: ContainerInfo[] = [];
  let loading = true;
  let error = '';

  onMount(async () => {
    try {
      const [containerList, serviceList] = await Promise.all([
        api.get<ContainerInfo[]>('/containers'),
        api.get<Service[]>('/services')
      ]);
      containers = containerList;
      services.set(serviceList);
    } catch (err) {
      error = err instanceof Error ? err.message : 'Failed to load dashboard';
    } finally {
      loading = false;
    }
  });

  const runningCount = () => containers.filter((c) => c.state === 'running').length;

  const stateClasses = (state: string): string =>
    state === 'running' ? 'bg-emerald-400/10 text-emerald-400' : 'bg-zinc-400/10 text-zinc-400';
</script>

<div class="space-y-6">
  <div>
    <h2 class="text-lg font-semibold text-white">Dashboard</h2>
    <p class="text-sm text-zinc-500">Overview of containers and services</p>
  </div>

  {#if error}
    <div class="p-4 bg-red-500/10 border border-red-500/20 rounded-sm">
      <p class="text-sm text-red-400">{error}</p>
    </div>
  {/if}

  <div class="grid grid-cols-2 lg:grid-cols-3 gap-4">
    <div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
      <div class="flex items-center justify-between mb-2">
        <span class="text-2xl">🐳</span>
        <span class="text-xs text-zinc-400">Containers</span>
      </div>
      <div class="text-2xl font-bold text-white">{loading ? '—' : containers.length}</div>
    </div>

    <div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
      <div class="flex items-center justify-between mb-2">
        <span class="text-2xl">✅</span>
        <span class="text-xs text-zinc-400">Running</span>
      </div>
      <div class="text-2xl font-bold text-emerald-400">{loading ? '—' : runningCount()}</div>
    </div>

    <div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
      <div class="flex items-center justify-between mb-2">
        <span class="text-2xl">🧩</span>
        <span class="text-xs text-zinc-400">Services</span>
      </div>
      <div class="text-2xl font-bold text-white">{loading ? '—' : $services.length}</div>
    </div>
  </div>

  <div class="bg-zinc-900 border border-zinc-800 rounded-sm overflow-hidden">
    <table class="w-full">
      <thead>
        <tr class="border-b border-zinc-800">
          <th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Name</th>
          <th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Image</th>
          <th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">State</th>
        </tr>
      </thead>
      <tbody>
        {#each containers as container (container.id)}
          <tr class="border-b border-zinc-800/50">
            <td class="px-4 py-2.5 text-sm text-white">{container.name}</td>
            <td class="px-4 py-2.5 text-sm text-zinc-400 font-mono">{container.image}</td>
            <td class="px-4 py-2.5">
              <span class="px-2 py-0.5 rounded text-xs font-medium {stateClasses(container.state)}">                {container.state}
              </span>
            </td>
          </tr>
        {:else}
          <tr>
            <td colspan="3" class="text-center py-8 text-zinc-600 text-sm">
              {loading ? 'Loading...' : 'No containers'}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
</div>
