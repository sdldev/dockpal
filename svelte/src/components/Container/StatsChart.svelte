<script lang="ts">
	// Live per-container stats — port of the legacy UI's container stats tab:
	// 4 value cards (CPU/Mem/RX/TX) + 3 Chart.js line charts (CPU, Memory,
	// Network RX+TX), 2.5s polling with a 30-point rolling window.
	// Instance-aware: polls /api/instances/:id/containers/:id/stats for remote.
	import { onMount, onDestroy } from 'svelte';
	import { get } from 'svelte/store';
	import type { ContainerStats } from '$lib/types/generated';
	import { selectedInstance } from '$lib/store';
	import LineChart from '../stats/LineChart.svelte';
	import { newStatBuffer, pushPoint, formatBytes, seriesMax, type StatBuffer } from '$lib/stats-history';

	interface Props {
		containerId: string;
		intervalMs?: number;
	}

	let { containerId, intervalMs = 2500 }: Props = $props();

	let stats: ContainerStats | null = $state(null);
	let live = $state(false);
	let pollId: ReturnType<typeof setInterval> | null = null;
	let inFlight = false;

	let cpuBuf = $state<StatBuffer>(newStatBuffer());
	let memBuf = $state<StatBuffer>(newStatBuffer());
	let rxBuf = $state<StatBuffer>(newStatBuffer());
	let txBuf = $state<StatBuffer>(newStatBuffer());

	const netMax = $derived(Math.max(seriesMax(rxBuf.values), seriesMax(txBuf.values), 1) * 1.2);

	function statsPath(instanceId: string): string {
		// NOTE: raw fetch (not api.get) — must include the /api prefix, otherwise
		// the SPA fallback returns index.html with status 200 and json() throws.
		return instanceId === 'local'
			? `/api/containers/${encodeURIComponent(containerId)}/stats`
			: `/api/instances/${encodeURIComponent(instanceId)}/containers/${encodeURIComponent(containerId)}/stats`;
	}

	async function fetchStats() {
		if (inFlight) return; // skip tick if previous request is still pending
		inFlight = true;
		try {
			// raw fetch with explicit auth header (api.get would trigger global
			// logout on transient 401s — we just want to skip the tick instead)
			const token = localStorage.getItem('dockpal_token');
			const response = await fetch(statsPath(get(selectedInstance) || 'local'), {
				headers: token ? { Authorization: `Bearer ${token}` } : {}
			});
			if (response.ok) {
				const data = (await response.json()) as ContainerStats;
				stats = data;
				live = true;
				pushPoint(cpuBuf, data.cpu_percent ?? 0);
				pushPoint(memBuf, data.memory_percent ?? 0);
				pushPoint(rxBuf, data.network_rx ?? 0);
				pushPoint(txBuf, data.network_tx ?? 0);
				// clone to trigger Svelte reactivity on mutated buffers
				cpuBuf = { ...cpuBuf };
				memBuf = { ...memBuf };
				rxBuf = { ...rxBuf };
				txBuf = { ...txBuf };
			} else if (response.status === 400 || response.status === 404) {
				live = false;
			}
		} catch {
			// transient — keep last values (legacy behavior)
		} finally {
			inFlight = false;
		}
	}

	onMount(() => {
		fetchStats();
		pollId = setInterval(fetchStats, intervalMs);
	});

	onDestroy(() => {
		if (pollId) clearInterval(pollId);
	});

	const memMB = () => ((stats?.memory_usage ?? 0) / 1024 / 1024).toFixed(1);
	const memLimitMB = () => ((stats?.memory_limit ?? 1) / 1024 / 1024).toFixed(1);
</script>

<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4 space-y-4">
	<div class="flex items-center justify-between">
		<h3 class="text-sm font-medium text-white">Container Stats</h3>
		<span class="text-xs {live ? 'text-emerald-400' : 'text-zinc-500'}">
			{live ? '● Live' : stats ? '○ Not running' : 'Loading...'}
		</span>
	</div>

	{#if stats}
		<!-- Live value cards (legacy parity) -->
		<div class="grid grid-cols-2 gap-3">
			<div class="bg-zinc-950 p-3 rounded-sm">
				<div class="text-xs text-zinc-500 mb-1">CPU Usage</div>
				<div class="text-lg font-semibold text-blue-400">{(stats.cpu_percent ?? 0).toFixed(2)}%</div>
			</div>
			<div class="bg-zinc-950 p-3 rounded-sm">
				<div class="text-xs text-zinc-500 mb-1">Memory</div>
				<div class="text-sm text-white">
					<span class="font-semibold text-emerald-400">{memMB()}</span>
					<span class="text-zinc-500"> / {memLimitMB()} MB</span>
				</div>
				<div class="text-xs text-zinc-500 mt-1">{(stats.memory_percent ?? 0).toFixed(1)}% of limit</div>
			</div>
			<div class="bg-zinc-950 p-3 rounded-sm">
				<div class="text-xs text-zinc-500 mb-1">Network RX</div>
				<div class="text-lg font-semibold text-violet-400">{formatBytes(stats.network_rx ?? 0)}</div>
			</div>
			<div class="bg-zinc-950 p-3 rounded-sm">
				<div class="text-xs text-zinc-500 mb-1">Network TX</div>
				<div class="text-lg font-semibold text-pink-400">{formatBytes(stats.network_tx ?? 0)}</div>
			</div>
		</div>

		<!-- Charts (legacy parity) -->
		<div class="space-y-4">
			<div>
				<div class="text-xs text-zinc-500 mb-1">CPU %</div>
				<LineChart
					series={[{ label: 'CPU %', color: '#3b82f6', data: cpuBuf.values }]}
					labels={cpuBuf.labels}
					yMin={0}
					yMax={100}
					height={110}
				/>
			</div>
			<div>
				<div class="text-xs text-zinc-500 mb-1">Memory %</div>
				<LineChart
					series={[{ label: 'Memory %', color: '#10b981', data: memBuf.values }]}
					labels={memBuf.labels}
					yMin={0}
					yMax={100}
					height={110}
				/>
			</div>
			<div>
				<div class="text-xs text-zinc-500 mb-1">Network I/O (cumulative bytes)</div>
				<LineChart
					series={[
						{ label: 'RX', color: '#a78bfa', data: rxBuf.values },
						{ label: 'TX', color: '#f472b6', data: txBuf.values }
					]}
					labels={rxBuf.labels}
					yMin={0}
					yMax={netMax}
					formatY={(v) => formatBytes(v, 0)}
					height={110}
				/>
			</div>
		</div>
	{:else}
		<div class="text-zinc-500 text-center py-8">
			{live === false ? 'No stats available (container not running)' : 'Loading...'}
		</div>
	{/if}
</div>
