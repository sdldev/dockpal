<script lang="ts">
	import { onMount } from 'svelte';
	import { getHealthStatus, startHealthMonitoring } from '$lib/api/health';

	let healthy = $state(false);
	let version = $state('');
	let uptime = $state(0);
	let loading = $state(true);
	let error = $state('');

	async function loadHealth() {
		try {
			const data = await getHealthStatus();
			healthy = data.status === 'healthy';
			version = data.version;
			uptime = data.uptime_seconds ?? 0;
			error = '';
		} catch (e) {
			healthy = false;
			error = e instanceof Error ? e.message : 'Unknown error';
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		loadHealth();
		startHealthMonitoring(60000); // Poll every minute
	});

	const uptimeFormatted = () => {
		const days = Math.floor(uptime / 86400);
		const hours = Math.floor((uptime % 86400) / 3600);
		const minutes = Math.floor((uptime % 3600) / 60);
		if (days > 0) return `${days}d ${hours}h ${minutes}m`;
		return `${hours}h ${minutes}m`;
	};
</script>

<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4 flex items-center justify-between">
	<div>
		<h3 class="text-sm font-medium text-white mb-1">System Status</h3>
		{#if loading}
			<span class="text-xs text-zinc-500">Checking…</span>
		{:else if healthy}
			<span class="text-xs text-emerald-400">✓ Healthy • Version {version}</span>
			<div class="text-xs text-zinc-500 mt-1">Uptime {uptimeFormatted()}</div>
		{:else}
			<span class="text-xs text-red-400">✗ Unhealthy</span>
			{#if error}
				<div class="text-xs text-zinc-600 mt-1">{error}</div>
			{/if}
		{/if}
	</div>

	<div class="w-4 h-4 rounded-full animate-pulse transition-colors"
		class:bg-emerald-400={healthy && !loading}
		class:bg-red-400={!healthy && !loading}
		class:bg-zinc-600={loading}>
	</div>
</div>
