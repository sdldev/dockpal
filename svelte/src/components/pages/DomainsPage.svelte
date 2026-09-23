<script lang="ts">
	import { api } from '$lib/api/client';
	import { addToast } from '$lib/store';
	import type { Domain } from '$lib/types/generated';
	import Button from '../ui/Button.svelte';

	let domains = $state<Domain[]>([]);
	let loading = $state(true);
	let error = $state('');
	let busy = $state(false);

	let name = $state('');
	let service = $state('');
	let port = $state(80);

	async function load() {
		loading = true;
		error = '';
		try {
			domains = await api.get<Domain[]>('/domains');
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load domains';
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		load();
	});

	async function addDomain() {
		if (!name.trim() || !service.trim() || !port) {
			addToast('Domain, service and port are required', 'error');
			return;
		}
		busy = true;
		try {
			await api.post('/domains', { name: name.trim(), service: service.trim(), port });
			addToast('Domain added', 'success');
			name = '';
			service = '';
			port = 80;
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Add failed', 'error');
		} finally {
			busy = false;
		}
	}

	async function deleteDomain(id: string) {
		try {
			await api.delete(`/domains/${id}`);
			addToast('Domain removed', 'success');
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Delete failed', 'error');
		}
	}
</script>

<div class="space-y-4">
	<div>
		<h2 class="text-lg font-semibold text-white">Domains</h2>
		<p class="text-sm text-zinc-500">Traefik reverse-proxy mappings (domain → service:port)</p>
	</div>

	<form class="flex flex-wrap gap-2" onsubmit={(e) => { e.preventDefault(); addDomain(); }}>
		<input
			type="text"
			bind:value={name}
			placeholder="app.example.com"
			class="flex-1 min-w-40 px-3 py-2 bg-zinc-900 border border-zinc-800 rounded-sm text-sm text-white"
		/>
		<input
			type="text"
			bind:value={service}
			placeholder="service name"
			class="flex-1 min-w-40 px-3 py-2 bg-zinc-900 border border-zinc-800 rounded-sm text-sm text-white"
		/>
		<input
			type="number"
			bind:value={port}
			min="1"
			max="65535"
			class="w-24 px-3 py-2 bg-zinc-900 border border-zinc-800 rounded-sm text-sm text-white font-mono"
		/>
		<Button type="submit" loading={busy}>Add domain</Button>
	</form>

	{#if error}
		<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm">
			<p class="text-sm text-red-400">{error}</p>
		</div>
	{/if}

	<div class="bg-zinc-900 border border-zinc-800 rounded-sm overflow-hidden">
		<table class="w-full">
			<thead>
				<tr class="border-b border-zinc-800">
					<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Domain</th>
					<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Service</th>
					<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Port</th>
					<th class="text-right px-4 py-2.5 text-xs font-medium text-zinc-500">Actions</th>
				</tr>
			</thead>
			<tbody>
				{#each domains as domain (domain.id)}
					<tr class="border-b border-zinc-800/50 hover:bg-zinc-800/20 transition-colors">
						<td class="px-4 py-2.5 text-sm text-white font-mono">{domain.domain}</td>
						<td class="px-4 py-2.5 text-sm text-zinc-400">{domain.service}</td>
						<td class="px-4 py-2.5 text-sm text-zinc-400 font-mono">{domain.port}</td>
						<td class="px-4 py-2.5 text-right">
							<Button variant="danger" size="sm" onclick={() => deleteDomain(domain.id)}>Delete</Button>
						</td>
					</tr>
				{:else}
					<tr>
						<td colspan="4" class="text-center py-8 text-zinc-600 text-sm">
							{loading ? 'Loading domains...' : 'No domains'}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
</div>
