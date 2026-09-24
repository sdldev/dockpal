<script lang="ts">
  // Dashboard with live host statistics — port of the legacy UI's dashboard:
  // system status + disk gauge, a 3-card row (CPU chart, Memory chart, info
  // list of running/stopped/containers/images).
  // Polls system/info every 2.5s with a 30-point rolling window (legacy parity).
  import { onMount } from 'svelte';
  import { api } from '../../lib/api/client';
  import { addToast } from '../../lib/store';
  import { selectedInstance } from '../../lib/store';
  import { get } from 'svelte/store';
  import type { ContainerInfo } from '../../lib/types/api';
  import type { SystemInfo } from '../../lib/types/generated';
  import HealthWidget from '../Dashboard/HealthWidget.svelte';
  import LineChart from '../stats/LineChart.svelte';
  import { newStatBuffer, pushPoint, dynamicYBounds, type StatBuffer } from '$lib/stats-history';

  let instanceId = get(selectedInstance) || 'local';

  let containers = $state<ContainerInfo[]>([]);
  let loading = $state(true);
  let error = $state('');
  let imageCount = $state(0);

  // --- host stats (polling) ---
  let sysInfo = $state<SystemInfo | null>(null);
  let cpuBuf = $state<StatBuffer>(newStatBuffer());
  let ramBuf = $state<StatBuffer>(newStatBuffer());
  let pollTimer: ReturnType<typeof setInterval> | null = null;
  let imageTimer: ReturnType<typeof setInterval> | null = null;

  // Tracks whether the live poll has gone silent. We still keep the last known
  // values (legacy behavior) but no longer pretend they are live: the header
  // badge tells the user the data is stale so they don't read a frozen
  // dashboard as "everything is fine".
  let stale = $state(false);
  let lastSuccessAt = $state<number | null>(null);
  let staleAgeLabel = $state('');

  const runningCount = $derived(containers.filter((c) => c.state === 'running').length);
  const stoppedCount = $derived(containers.length - runningCount);

  const cpuBounds = $derived(dynamicYBounds(cpuBuf.values));
  const ramBounds = $derived(dynamicYBounds(ramBuf.values));

  const gaugeColor = (pct: number, normal: string) =>
    pct > 85 ? 'bg-red-500' : pct > 70 ? 'bg-amber-500' : normal;

  function systemInfoPath(id: string): string {
    return id === 'local' ? '/system/info' : `/instances/${encodeURIComponent(id)}/system/info`;
  }

  function containersPath(id: string): string {
    return id === 'local' ? '/containers' : `/instances/${encodeURIComponent(id)}/containers`;
  }

  function imagesPath(id: string): string {
    return id === 'local' ? '/images' : `/instances/${encodeURIComponent(id)}/images`;
  }

  function formatAge(ms: number): string {
    const seconds = Math.round(ms / 1000);
    if (seconds < 60) return `${seconds}s`;
    const minutes = Math.round(seconds / 60);
    if (minutes < 60) return `${minutes}m`;
    return `${Math.round(minutes / 60)}h`;
  }

  async function pollSystemInfo() {
    try {
      const info = await api.get<SystemInfo>(systemInfoPath(instanceId));
      sysInfo = info;
      pushPoint(cpuBuf, info.cpu_percent ?? 0);
      pushPoint(ramBuf, info.total_ram > 0 ? (info.used_ram / info.total_ram) * 100 : 0);
      cpuBuf = { labels: [...cpuBuf.labels], values: [...cpuBuf.values] };
      ramBuf = { labels: [...ramBuf.labels], values: [...ramBuf.values] };
      lastSuccessAt = Date.now();
      stale = false;
      staleAgeLabel = '';
    } catch {
      // Keep last values (legacy behavior), but flag staleness so the UI can
      // tell the user this is no longer live data. Toast fires only on the
      // online → offline transition to avoid spamming every 2.5s poll.
      if (lastSuccessAt) staleAgeLabel = formatAge(Date.now() - lastSuccessAt);
      if (!stale) {
        stale = true;
        addToast(
          'Live data unavailable — the server stopped responding. Showing last known values.',
          'error'
        );
      }
    }
  }

  async function loadContainers() {
    try {
      containers = await api.get<ContainerInfo[]>(containersPath(instanceId));
    } catch (err) {
      error = err instanceof Error ? err.message : 'Failed to load containers';
    }
  }

  async function loadImageCount() {
    try {
      const images = await api.get<unknown[]>(imagesPath(instanceId));
      imageCount = Array.isArray(images) ? images.length : 0;
    } catch {
      // keep last count
    }
  }

  onMount(() => {
    instanceId = get(selectedInstance) || 'local';
    (async () => {
      await Promise.all([loadContainers(), loadImageCount(), pollSystemInfo()]);
      loading = false;
    })();
    pollTimer = setInterval(pollSystemInfo, 2500);
    imageTimer = setInterval(loadImageCount, 30000);
    return () => {
      if (pollTimer) clearInterval(pollTimer);
      if (imageTimer) clearInterval(imageTimer);
    };
  });
</script>

<div class="space-y-6">
  <div>
    <h2 class="text-lg font-semibold text-white">Dashboard</h2>
    <p class="text-sm text-zinc-500">
      {#if sysInfo}
        Server: {sysInfo.hostname} • Docker {sysInfo.docker_version} • {sysInfo.cpu_cores} cores
      {:else}
        Overview of containers and services
      {/if}
    </p>
    {#if stale}
      <div
        class="mt-3 inline-flex items-center gap-2 px-3 py-1.5 text-xs text-amber-300 bg-amber-500/10 border border-amber-500/30 rounded-sm"
        role="status"
        aria-live="polite"
      >
        <span aria-hidden="true">⚠</span>
        <span>
          Live data unavailable — showing last known values{lastSuccessAt
            ? ` (updated ${staleAgeLabel} ago)`
            : ''}. Polling will resume automatically when the server responds.
        </span>
      </div>
    {/if}
  </div>

  <div class="grid gap-4 md:grid-cols-2">
    <HealthWidget />
    {#if sysInfo}
      {@const diskPct = sysInfo.total_disk > 0 ? (sysInfo.used_disk / sysInfo.total_disk) * 100 : 0}
      <div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4 flex items-center">
        <div class="w-full">
          <div class="flex items-center justify-between mb-2">
            <span class="text-sm font-medium text-white">Disk</span>
            <span class="text-sm text-zinc-400">
              {(sysInfo.used_disk / 1024 ** 3).toFixed(1)} / {(sysInfo.total_disk / 1024 ** 3).toFixed(1)} GB
              <span class="text-zinc-600">({diskPct.toFixed(1)}%)</span>
            </span>
          </div>
          <div class="h-2 bg-zinc-800 rounded-full overflow-hidden">
            <div class="h-full rounded-full transition-all duration-500 {gaugeColor(diskPct, 'bg-violet-500')}"
                 style="width: {Math.min(diskPct, 100)}%"></div>
          </div>
        </div>
      </div>
    {/if}
  </div>

  {#if error}
    <div class="p-4 bg-red-500/10 border border-red-500/20 rounded-sm">
      <p class="text-sm text-red-400">{error}</p>
    </div>
  {/if}

  {#if sysInfo}
    <!-- Live charts + info (legacy parity) -->
    <div class="grid gap-4 md:grid-cols-3">
      <div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
        <div class="flex items-center justify-between mb-2">
          <h3 class="text-sm font-medium text-zinc-300">CPU Usage</h3>
          <span class="text-sm font-semibold text-blue-400">{(cpuBuf.values.at(-1) ?? 0).toFixed(1)}%</span>
        </div>
        <div
          role="img"
          aria-label="CPU usage over time, currently {(cpuBuf.values.at(-1) ?? 0).toFixed(1)} percent"
        >
          <LineChart
            series={[{ label: 'CPU %', color: '#3b82f6', data: cpuBuf.values }]}
            labels={cpuBuf.labels}
            yMin={cpuBounds.min}
            yMax={cpuBounds.max}
            height={144}
          />
        </div>
      </div>
      <div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
        <div class="flex items-center justify-between mb-2">
          <h3 class="text-sm font-medium text-zinc-300">Memory Usage</h3>
          <span class="text-sm font-semibold text-emerald-400">{(ramBuf.values.at(-1) ?? 0).toFixed(1)}%</span>
        </div>
        <div
          role="img"
          aria-label="Memory usage over time, currently {(ramBuf.values.at(-1) ?? 0).toFixed(1)} percent"
        >
          <LineChart
            series={[{ label: 'RAM %', color: '#10b981', data: ramBuf.values }]}
            labels={ramBuf.labels}
            yMin={ramBounds.min}
            yMax={ramBounds.max}
            height={144}
          />
        </div>
      </div>

      <div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
        <h3 class="text-sm font-medium text-zinc-300 mb-3">Info</h3>
        <ul role="list" class="space-y-2.5">
          <li class="flex items-center justify-between">
            <span class="text-sm text-zinc-400">Running</span>
            <span class="text-sm font-semibold text-emerald-400">{loading ? '—' : runningCount}</span>
          </li>
          <li class="flex items-center justify-between">
            <span class="text-sm text-zinc-400">Stopped</span>
            <span class="text-sm font-semibold text-zinc-300">{loading ? '—' : stoppedCount}</span>
          </li>
          <li class="flex items-center justify-between">
            <span class="text-sm text-zinc-400">Containers</span>
            <span class="text-sm font-semibold text-white">{loading ? '—' : containers.length}</span>
          </li>
          <li class="flex items-center justify-between">
            <span class="text-sm text-zinc-400">Images</span>
            <span class="text-sm font-semibold text-white">{loading ? '—' : imageCount}</span>
          </li>
        </ul>
      </div>
    </div>
  {/if}
</div>
