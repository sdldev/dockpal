<script lang="ts">
	import { api } from '$lib/api/client';
	import type { SystemInfo } from '$lib/types/generated';
	import Button from '../ui/Button.svelte';

	let info = $state<SystemInfo | null>(null);
	let loading = $state(true);
	let error = $state('');

	async function load() {
		loading = true;
		error = '';
		try {
			info = await api.get<SystemInfo>('/system/info');
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load system info';
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		load();
	});

	function formatBytes(bytes: number) {
		if (!bytes) return '0 B';
		const units = ['B', 'KB', 'MB', 'GB', 'TB'];
		const idx = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
		return `${(bytes / Math.pow(1024, idx)).toFixed(1)} ${units[idx]}`;
	}

	function pct(used: number, total: number) {
		return total > 0 ? ((used / total) * 100).toFixed(1) : '0';
	}
</script>

{#if error}
	<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm">
		<p class="text-sm text-red-400">{error}</p>
	</div>
{/if}

{#if loading && !info}
	<p class="text-sm text-zinc-500 text-center py-8">Loading system info...</p>
{:else if info}
	<div class="space-y-4">
		<div class="flex justify-end">
			<Button variant="secondary" size="sm" onclick={load}>Refresh</Button>
		</div>

		<div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
			<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
				<div class="text-xs text-zinc-500 mb-1">Hostname</div>
				<div class="text-sm text-white font-mono">{info.hostname}</div>
				<div class="text-xs text-zinc-500 mt-1">{info.os}</div>
			</div>
			<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
				<div class="text-xs text-zinc-500 mb-1">CPU</div>
				<div class="text-sm text-white">{info.cpu_cores} cores @ {info.cpu_percent.toFixed(1)}%</div>
			</div>
			<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
				<div class="text-xs text-zinc-500 mb-1">RAM</div>
				<div class="text-sm text-white">
					{formatBytes(info.used_ram)} / {formatBytes(info.total_ram)}
				</div>
				<div class="text-xs text-zinc-500 mt-1">{pct(info.used_ram, info.total_ram)}% used</div>
			</div>
			<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
				<div class="text-xs text-zinc-500 mb-1">Disk</div>
				<div class="text-sm text-white">
					{formatBytes(info.used_disk)} / {formatBytes(info.total_disk)}
				</div>
				<div class="text-xs text-zinc-500 mt-1">{pct(info.used_disk, info.total_disk)}% used</div>
			</div>
		</div>

		<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
			<div class="text-xs text-zinc-500 mb-1">Docker version</div>
			<div class="text-sm text-white font-mono">{info.docker_version}</div>
		</div>
	</div>
{/if}
