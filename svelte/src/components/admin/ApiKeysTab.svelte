<script lang="ts">
	import { api } from '$lib/api/client';
	import { addToast } from '$lib/store';
	import type { ApiKey, ApiKeyCreated } from '$lib/types/generated';
	import Button from '../ui/Button.svelte';

	let keys = $state<ApiKey[]>([]);
	let loading = $state(true);
	let error = $state('');
	let name = $state('');
	let role = $state('viewer');
	let creating = $state(false);
	let newKey = $state<ApiKeyCreated | null>(null);

	const roles: Array<'admin' | 'operator' | 'viewer'> = ['admin', 'operator', 'viewer'];

	async function load() {
		loading = true;
		error = '';
		try {
			keys = await api.get<ApiKey[]>('/api-keys');
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load API keys';
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		load();
	});

	async function createKey() {
		if (!name.trim()) return;
		creating = true;
		try {
			newKey = await api.post<ApiKeyCreated>('/api-keys', { name: name.trim(), role });
			name = '';
			addToast('API key created', 'success');
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Create failed', 'error');
		} finally {
			creating = false;
		}
	}

	async function deleteKey(id: string) {
		try {
			await api.delete(`/api-keys/${id}`);
			addToast('API key deleted', 'success');
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Delete failed', 'error');
		}
	}

	async function copyKey(value: string) {
		try {
			await navigator.clipboard.writeText(value);
			addToast('Copied to clipboard', 'info');
		} catch {
			addToast('Clipboard unavailable — copy manually', 'error');
		}
	}

	function formatTime(ts: number) {
		return new Date(ts * 1000).toLocaleString('en-US', { hour12: false });
	}
</script>

{#if newKey}
	<div class="p-4 bg-emerald-500/10 border border-emerald-500/20 rounded-sm space-y-2">
		<p class="text-sm text-emerald-400 font-medium">API key created — copy it now, it will not be shown again</p>
		<code class="block text-sm text-white font-mono break-all bg-zinc-950 p-2 rounded-sm">{newKey.key}</code>
		<div class="flex gap-2">
			<Button variant="primary" size="sm" onclick={() => copyKey(newKey!.key)}>Copy</Button>
			<Button variant="secondary" size="sm" onclick={() => { newKey = null; }}>Dismiss</Button>
		</div>
	</div>
{/if}

<form class="flex gap-2" onsubmit={(e) => { e.preventDefault(); createKey(); }}>
	<input
		type="text"
		bind:value={name}
		placeholder="Key name"
		class="flex-1 px-3 py-2 bg-zinc-900 border border-zinc-800 rounded-sm text-sm text-white"
	/>
	<select
		bind:value={role}
		class="px-3 py-2 bg-zinc-900 border border-zinc-800 rounded-sm text-sm text-white"
	>
		{#each roles as r}
			<option value={r}>{r}</option>
		{/each}
	</select>
	<Button type="submit" loading={creating}>Create</Button>
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
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Name</th>
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Role</th>
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Created</th>
				<th class="text-right px-4 py-2.5 text-xs font-medium text-zinc-500">Actions</th>
			</tr>
		</thead>
		<tbody>
			{#each keys as key (key.id)}
				<tr class="border-b border-zinc-800/50 hover:bg-zinc-800/20 transition-colors">
					<td class="px-4 py-2.5 text-sm text-white">{key.name}</td>
					<td class="px-4 py-2.5">
						<span class="px-2 py-0.5 rounded text-xs font-medium bg-blue-400/10 text-blue-400 capitalize">{key.role}</span>
					</td>
					<td class="px-4 py-2.5 text-sm text-zinc-500">{formatTime(key.created_at)}</td>
					<td class="px-4 py-2.5 text-right">
						<Button variant="danger" size="sm" onclick={() => deleteKey(key.id)}>Delete</Button>
					</td>
				</tr>
			{:else}
				<tr>
					<td colspan="4" class="text-center py-8 text-zinc-600 text-sm">
						{loading ? 'Loading keys...' : 'No API keys'}
					</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>
