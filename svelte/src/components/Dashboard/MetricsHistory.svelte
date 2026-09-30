<script lang="ts">
  // Metrics history panel — time-series charts over the recorded host
  // metrics with a range selector (Live rolling buffer, or 1h/12h from the
  // recorder). This is what lets the dashboard answer "when did the server
  // spike?", not just "what is happening right now".
  //
  // Data comes from GET /api/instances/:id/metrics/history (backend
  // MetricsHistoryRecorder, 30s sampling, uniform downsampling server-side).
  import { api } from '$lib/api/client';
  import { selectedInstance } from '$lib/store';
  import { get } from 'svelte/store';
  import LineChart from '../stats/LineChart.svelte';

  interface HistorySample {
    ts: number;
    cpu_percent: number;
    used_ram: number;
    total_ram: number;
    used_disk: number;
    total_disk: number;
    network_rx_bps: number;
    network_tx_bps: number;
  }

  interface HistoryResponse {
    instance_id: string;
    from: number;
    to: number;
    samples: HistorySample[];
  }

  // Range options. `live` keeps the old in-page 2.5s rolling buffer look
  // (no history fetch); 1h/12h query the 30s recorder. Longer ranges were
  // removed: at 30d granularity a 300-point downsample flattens spikes into
  // an unreadable average, which misrepresents the server's real state.
  const ranges = [
    { id: 'live', label: 'Live', hours: 0 },
    { id: '1h', label: '1h', hours: 1 },
    { id: '12h', label: '12h', hours: 12 }
  ] as const;
  type RangeId = (typeof ranges)[number]['id'];

  interface Props {
    instanceId?: string;
    // Live buffers pushed by the dashboard's own 2.5s poll (used for the
    // 'live' range only).
    liveCpu?: number[];
    liveRam?: number[];
  }
  let { instanceId, liveCpu = [], liveRam = [] }: Props = $props();

  const id = $derived(instanceId || get(selectedInstance) || 'local');

  let activeRange = $state<RangeId>('1h');
  let loading = $state(false);
  let error = $state('');
  let samples = $state<HistorySample[]>([]);

  const rangesMap = $derived(Object.fromEntries(ranges.map((r) => [r.id, r])));

  function historyPath(from: number, to: number): string {
    return `/instances/${encodeURIComponent(id)}/metrics/history?from=${from}&to=${to}&max_points=300`;
  }

  async function loadHistory() {
    if (activeRange === 'live') return;
    const { hours } = rangesMap[activeRange];
    const now = Math.floor(Date.now() / 1000);
    const from = now - hours * 3600;
    loading = true;
    error = '';
    try {
      const resp = await api.get<HistoryResponse>(historyPath(from, now));
      samples = resp.samples ?? [];
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to load metrics history';
      samples = [];
    } finally {
      loading = false;
    }
  }

  // Re-load when the range or instance changes.
  $effect(() => {
    void activeRange;
    void id;
    loadHistory();
    // Refresh history charts every 30s so the right edge stays current.
    const t = setInterval(() => {
      if (activeRange !== 'live') loadHistory();
    }, 30000);
    return () => clearInterval(t);
  });

  // --- chart data (from history, or from live buffers) -------------------
  const usingLive = $derived(activeRange === 'live');

  const cpuData = $derived(
    usingLive ? liveCpu : samples.map((s) => s.cpu_percent)
  );
  const ramData = $derived(
    usingLive
      ? liveRam
      : samples.map((s) => (s.total_ram > 0 ? (s.used_ram / s.total_ram) * 100 : 0))
  );
  const diskData = $derived(
    samples.map((s) => (s.total_disk > 0 ? (s.used_disk / s.total_disk) * 100 : 0))
  );

  const labels = $derived(
    usingLive
      ? cpuData.map((_, i) => `${i}`)
      : samples.map((s) => formatTimeLabel(s.ts, activeRange))
  );

  function formatTimeLabel(ts: number, _range: RangeId): string {
    const d = new Date(ts * 1000);
    const time = d.toLocaleTimeString('en-US', { hour12: false, hour: '2-digit', minute: '2-digit' });
    // Live/1h use time-of-day only; 12h adds the date so points on other
    // days stay unambiguous.
    if (_range === '12h') {
      const day = `${d.getMonth() + 1}/${d.getDate()}`;
      return `${day} ${time}`;
    }
    return time;
  }

  const rxData = $derived(samples.map((s) => s.network_rx_bps ?? 0));
  const txData = $derived(samples.map((s) => s.network_tx_bps ?? 0));

  const latest = $derived({
    cpu: cpuData.at(-1) ?? 0,
    ram: ramData.at(-1) ?? 0,
    disk: diskData.at(-1) ?? 0,
    rx: rxData.at(-1) ?? 0,
    tx: txData.at(-1) ?? 0
  });

  // Peak over the visible range — the whole point of history: see the max,
  // not just the present.
  const peak = $derived({
    cpu: cpuData.length ? Math.max(...cpuData) : 0,
    ram: ramData.length ? Math.max(...ramData) : 0,
    disk: diskData.length ? Math.max(...diskData) : 0,
    net: rxData.length || txData.length
      ? Math.max(...rxData, ...txData, 0)
      : 0
  });

  const noHistory = $derived(!usingLive && !loading && samples.length === 0);

  function formatBps(bps: number): string {
    if (bps < 1024) return `${bps.toFixed(0)} B/s`;
    if (bps < 1024 * 1024) return `${(bps / 1024).toFixed(1)} KB/s`;
    if (bps < 1024 * 1024 * 1024) return `${(bps / 1024 / 1024).toFixed(1)} MB/s`;
    return `${(bps / 1024 / 1024 / 1024).toFixed(2)} GB/s`;
  }
</script>

<div class="space-y-4">
  <!-- Range selector -->
  <div class="flex items-center justify-between">
    <div class="flex gap-1 bg-zinc-900 border border-zinc-800 rounded-sm p-1">
      {#each ranges as range}
        <button
          class="px-3 py-1.5 text-xs font-medium rounded-sm transition-colors"
          class:bg-white={activeRange === range.id}
          class:text-zinc-900={activeRange === range.id}
          class:text-zinc-400={activeRange !== range.id}
          class:hover:text-white={activeRange !== range.id}
          onclick={() => { activeRange = range.id; }}
        >
          {range.label}
        </button>
      {/each}
    </div>
    <span class="text-xs text-zinc-600">
      {#if usingLive}
        rolling buffer · 30 points · 2.5s
      {:else if loading}
        loading…
      {:else if samples.length > 0}
        {samples.length} points · 30s samples
      {/if}
    </span>
  </div>

  {#if error}
    <div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm text-sm text-red-400">{error}</div>
  {/if}

  {#if noHistory}
    <div class="p-4 bg-zinc-900 border border-zinc-800 rounded-sm text-sm text-zinc-500 text-center">
      No recorded history yet for this server — the recorder runs every 30 seconds, so
      charts will appear shortly.
    </div>
  {:else}
    <div class="grid gap-4 md:grid-cols-3">
      <!-- CPU -->
      <div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
        <div class="flex items-center justify-between mb-1">
          <h3 class="text-sm font-medium text-zinc-300">CPU</h3>
          <div class="text-right">
            <span class="text-sm font-semibold text-blue-400">{latest.cpu.toFixed(1)}%</span>
            {#if !usingLive && peak.cpu > 0}
              <span class="block text-[10px] text-zinc-600">peak {peak.cpu.toFixed(1)}%</span>
            {/if}
          </div>
        </div>
        <LineChart
          series={[{ label: 'CPU %', color: '#3b82f6', data: cpuData }]}
          {labels}
          yMin={0}
          height={150}
        />
      </div>

      <!-- Memory -->
      <div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
        <div class="flex items-center justify-between mb-1">
          <h3 class="text-sm font-medium text-zinc-300">Memory</h3>
          <div class="text-right">
            <span class="text-sm font-semibold text-emerald-400">{latest.ram.toFixed(1)}%</span>
            {#if !usingLive && peak.ram > 0}
              <span class="block text-[10px] text-zinc-600">peak {peak.ram.toFixed(1)}%</span>
            {/if}
          </div>
        </div>
        <LineChart
          series={[{ label: 'RAM %', color: '#10b981', data: ramData }]}
          {labels}
          yMin={0}
          height={150}
        />
      </div>

      <!-- Disk -->
      <div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
        <div class="flex items-center justify-between mb-1">
          <h3 class="text-sm font-medium text-zinc-300">Disk</h3>
          <div class="text-right">
            <span class="text-sm font-semibold text-violet-400">{latest.disk.toFixed(1)}%</span>
            {#if !usingLive && peak.disk > 0}
              <span class="block text-[10px] text-zinc-600">peak {peak.disk.toFixed(1)}%</span>
            {/if}
          </div>
        </div>
        <LineChart
          series={[{ label: 'Disk %', color: '#8b5cf6', data: diskData }]}
          {labels}
          yMin={0}
          height={150}
        />
      </div>

      <!-- Network -->
      <div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
        <div class="flex items-center justify-between mb-1">
          <h3 class="text-sm font-medium text-zinc-300">Network</h3>
          <div class="text-right text-xs">
            <span class="text-sky-400 font-semibold">↓ {formatBps(latest.rx)}</span>
            <span class="text-amber-400 font-semibold ml-2">↑ {formatBps(latest.tx)}</span>
            {#if !usingLive && peak.net > 0}
              <span class="block text-[10px] text-zinc-600">peak {formatBps(peak.net)}</span>
            {/if}
          </div>
        </div>
        {#if usingLive}
          <div class="flex h-[150px] items-center justify-center text-xs text-zinc-600">
            Network history uses the recorder — switch to 1h or 12h
          </div>
        {:else}
          <LineChart
            series={[
              { label: 'Download', color: '#0ea5e9', data: rxData },
              { label: 'Upload', color: '#f59e0b', data: txData }
            ]}
            {labels}
            yMin={0}
            formatY={formatBps}
            height={150}
          />
        {/if}
      </div>
    </div>
  {/if}
</div>
