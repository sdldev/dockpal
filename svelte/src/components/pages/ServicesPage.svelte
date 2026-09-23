<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api/client';
	import { addToast } from '$lib/store';
	import type { ServiceRecord } from '$lib/types/generated';
	import Button from '../ui/Button.svelte';

	let serviceList = $state<ServiceRecord[]>([]);
	let loading = $state(true);
	let error = $state('');
	let busy = $state<string | null>(null);

	onMount(load);

	async function load() {
		loading = true;
		error = '';
		try {
			serviceList = await api.get<ServiceRecord[]>('/services');
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load services';
		} finally {
			loading = false;
		}
	}

	async function deleteService(id: string) {
		busy = id;
		try {
			await api.delete(`/services/${id}`);
			addToast('Service deleted', 'success');
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Delete failed', 'error');
		} finally {
			busy = null;
		}
	}

	function statusBadge(status: string) {
		switch (status.toLowerCase()) {
			case 'running': return 'bg-emerald-400/10 text-emerald-400';
			case 'stopped': return 'bg-zinc-400/10 text-zinc-400';
			case 'degraded': return 'bg-amber-400/10 text-amber-400';
			case 'error': return 'bg-red-400/10 text-red-400';
			default: return 'bg-blue-400/10 text-blue-400';
		}
	}

	function formatTime(ts: number) {
		return new Date(ts * 1000).toLocaleString('en-US', { hour12: false });
	}
</script>

<div class="space-y-4">
	<div class="flex items-center justify-between">
		<h2 class="text-lg font-semibold text-white">Services</h2>
		<Button variant="secondary" size="sm" onclick={load}>Refresh</Button>
	</div>

	{#if error}
		<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm">
			<p class="text-sm text-red-400">{error}</p>
		</div>
	{/if}

	<div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
		{#each serviceList as svc (svc.id)}
			<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
				<div class="flex items-start justify-between mb-2">
					<div>
						<h3 class="text-sm font-semibold text-white">{svc.name}</h3>
						<span class={`px-2 py-0.5 rounded text-xs font-medium ${statusBadge(svc.type)}`}>
							{svc.type}
						</span>
					</div>
					<Button variant="danger" size="sm" disabled={busy === svc.id} onclick={() => deleteService(svc.id)}>Delete</Button>
				</div>
				{#if svc.domain}
					<p class="text-xs text-zinc-400 mb-2">
						Domain: <code class="text-blue-400">{svc.domain}</code>
					</p>
				{/if}
				{#if svc.repo}
					<p class="text-xs text-zinc-400 mb-2">
						Repo: <code class="text-blue-400 font-mono">{svc.repo}</code>
					</p>
				{/if}
				<div class="text-xs text-zinc-500 mt-2">
					Created: {formatTime(svc.created_at)}
				</div>
			</div>
		{:else}
			<div class="col-span-full text-center py-12 text-zinc-600 text-sm">
				{loading ? 'Loading services...' : 'No services'}
			</div>
		{/each}
	</div>
</div>
