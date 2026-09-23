<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api/client';
	import type { ContainerInfo } from '$lib/types/api';
	import { formatPort } from '$lib/format';
	import { navigate } from '$lib/router';
	import StatsChart from '../Container/StatsChart.svelte';
	import Button from '../ui/Button.svelte';

	let containers: ContainerInfo[] = $state([]);
	let selectedContainerId: string | null = $state(null);
	let loading = $state(true);
	let error = $state('');
	let actionBusy = $state<string | null>(null);

	onMount(async () => {
		try {
			containers = await api.get<ContainerInfo[]>('/containers');
			if (!loading) return; // don't show old page after navigation
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load containers';
		} finally {
			loading = false;
		}
	});

	async function runAction(action: 'start' | 'stop' | 'restart', id: string) {
		actionBusy = id;
		try {
			const endpoint = `/api/containers/${id}/${action}`;
			await api.post(endpoint);
			// Refresh list
			const updated = await api.get<ContainerInfo[]>(`/containers`);
			containers = updated.map(c => c.id === id ? { ...c, state: action === 'stop' ? 'exited' : 'running' } : c);
		} catch (e) {
			console.error(`Failed to ${action}:`, e);
		} finally {
			actionBusy = null;
		}
	}

	function toggleDetail(id: string) {
		selectedContainerId = selectedContainerId === id ? null : id;
	}
</script>

<div class="space-y-4">
	<h2 class="text-lg font-semibold text-white">Containers</h2>

	{#if error}
		<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm">
			<p class="text-sm text-red-400">{error}</p>
		</div>
	{/if}

	<table class="w-full bg-zinc-900 border border-zinc-800 rounded-sm overflow-hidden">
		<thead>
			<tr class="border-b border-zinc-800">
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Name</th>
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Image</th>
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">State</th>
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Ports</th>
				<th class="right px-4 py-2.5 text-xs font-medium text-zinc-500">Actions</th>
			</tr>
		</thead>
		<tbody>
			{#each containers as container (container.id)}
				<tr class="border-b border-zinc-800/50 hover:bg-zinc-800/20 transition-colors">
						<td class="px-4 py-2.5 text-sm text-white">
							<button class="hover:text-sky-400 hover:underline" onclick={() => navigate('container-detail', { id: container.id })}>{container.name}</button>
						</td>
					<td class="px-4 py-2.5 text-sm text-zinc-400 font-mono">{container.image}</td>
					<td class="px-4 py-2.5">
						<span class={`px-2 py-0.5 rounded text-xs font-medium ${(container.state === 'running' ? 'bg-emerald-400/10 text-emerald-400' : 'bg-zinc-400/10 text-zinc-400')}`}>
							{container.state}
						</span>
					</td>
					<td class="px-4 py-2.5 text-sm text-zinc-400 font-mono">
						{#if Array.isArray(container.ports) && container.ports.length > 0}
							{#each container.ports.slice(0, 2) as port}<code class="mr-2 text-xs">{formatPort(port)}</code>{/each}
							{#if container.ports.length > 2}<span class="text-zinc-600 text-xs">+{container.ports.length - 2}</span>{/if}
						{:else}
							<span class="text-zinc-600 text-xs">No ports</span>
						{/if}
					</td>
					<td class="px-4 py-2.5 space-x-2">
						<Button variant="secondary" size="sm" onclick={() => toggleDetail(container.id)}>
							{selectedContainerId === container.id ? 'Close' : 'Details'}
						</Button>
						<Button variant="primary" size="sm" disabled={actionBusy === container.id} onclick={() => runAction('start', container.id)}>Start</Button>
						<Button variant="danger" size="sm" disabled={actionBusy === container.id} onclick={() => runAction('stop', container.id)}>Stop</Button>
						<Button variant="secondary" size="sm" disabled={actionBusy === container.id} onclick={() => runAction('restart', container.id)}>Restart</Button>
					</td>
				</tr>
			{:else}
				<tr><td colspan="5" class="text-center py-8 text-zinc-600">{loading ? 'Loading...' : 'No containers found'}</td></tr>
			{/each}
		</tbody>
	</table>

	<!-- Expandable detail & stats panel -->
	{#if selectedContainerId && containers.length > 0}
		<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-6">
			<h3 class="text-md font-semibold text-white mb-4">Container Details & Stats</h3>
			<StatsChart containerId={selectedContainerId} />
		</div>
	{/if}
</div>
