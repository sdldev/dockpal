<script lang="ts">
	import { api } from '$lib/api/client';
	import { addToast } from '$lib/store';
	import type { RegistryCredential, RegistryTestResult } from '$lib/types/generated';
	import Button from '../ui/Button.svelte';
	import ConfirmDialog from '../ui/ConfirmDialog.svelte';

	let creds = $state<RegistryCredential[]>([]);
	let loading = $state(true);
	let error = $state('');
	let busy = $state(false);
	let testing = $state<string | null>(null);
	let registry = $state('');
	let username = $state('');
	let token = $state('');
	let testResults = $state<Record<string, RegistryTestResult>>({});
	let pendingDelete = $state<RegistryCredential | null>(null);

	async function load() {
		loading = true;
		error = '';
		try {
			creds = await api.get<RegistryCredential[]>('/registries');
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load registries';
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		load();
	});

	async function addRegistry() {
		if (!registry.trim() || !username.trim() || !token.trim()) {
			addToast('Registry, username and token are all required', 'error');
			return;
		}
		busy = true;
		try {
			await api.post('/registries', {
				registry: registry.trim(),
				username: username.trim(),
				token: token.trim()
			});
			addToast('Registry credential saved', 'success');
			registry = '';
			username = '';
			token = '';
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Add failed', 'error');
		} finally {
			busy = false;
		}
	}

	async function testRegistry(id: string) {
		testing = id;
		try {
			const result = await api.post<RegistryTestResult>(`/registries/${id}/test`);
			testResults = { ...testResults, [id]: result };
			addToast(result.status === 'success' ? 'Registry reachable' : `Test: ${result.message}`, result.status === 'success' ? 'success' : 'error');
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Test failed', 'error');
		} finally {
			testing = null;
		}
	}

	async function deleteRegistry() {
		const target = pendingDelete;
		if (!target) return;
		try {
			await api.delete(`/registries/${target.id}`);
			addToast('Registry removed', 'success');
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Delete failed', 'error');
		} finally {
			pendingDelete = null;
		}
	}

	function statusClass(status: string) {
		if (status === 'valid') return 'bg-emerald-400/10 text-emerald-400';
		if (status === 'invalid') return 'bg-red-400/10 text-red-400';
		return 'bg-zinc-400/10 text-zinc-400';
	}
</script>

<form class="grid grid-cols-1 sm:grid-cols-4 gap-2" onsubmit={(e) => { e.preventDefault(); addRegistry(); }}>
	<input
		type="text"
		bind:value={registry}
		placeholder="ghcr.io"
		class="px-3 py-2 bg-zinc-900 border border-zinc-800 rounded-sm text-sm text-white"
	/>
	<input
		type="text"
		bind:value={username}
		placeholder="Username"
		class="px-3 py-2 bg-zinc-900 border border-zinc-800 rounded-sm text-sm text-white"
	/>
	<input
		type="password"
		bind:value={token}
		placeholder="Token / password"
		class="px-3 py-2 bg-zinc-900 border border-zinc-800 rounded-sm text-sm text-white"
	/>
	<Button type="submit" loading={busy}>Add registry</Button>
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
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Registry</th>
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Username</th>
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Token</th>
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Status</th>
				<th class="text-right px-4 py-2.5 text-xs font-medium text-zinc-500">Actions</th>
			</tr>
		</thead>
		<tbody>
			{#each creds as cred (cred.id)}
				<tr class="border-b border-zinc-800/50 hover:bg-zinc-800/20 transition-colors">
					<td class="px-4 py-2.5 text-sm text-white font-mono">{cred.registry}</td>
					<td class="px-4 py-2.5 text-sm text-zinc-400">{cred.username}</td>
					<td class="px-4 py-2.5 text-sm text-zinc-500 font-mono">{cred.masked_token}</td>
					<td class="px-4 py-2.5">
						<span class="px-2 py-0.5 rounded text-xs font-medium {statusClass(cred.status)}">{cred.status}</span>
						{#if testResults[cred.id]}
							<span class="text-xs text-zinc-500 ml-2">{testResults[cred.id].message}</span>
						{/if}
					</td>
					<td class="px-4 py-2.5 text-right space-x-2">
						<Button variant="secondary" size="sm" loading={testing === cred.id} onclick={() => testRegistry(cred.id)}>Test</Button>
						<Button variant="danger" size="sm" onclick={() => (pendingDelete = cred)}>Delete</Button>
					</td>
				</tr>
			{:else}
				<tr>
					<td colspan="5" class="text-center py-8 text-zinc-600 text-sm">
						{loading ? 'Loading registries...' : 'No registry credentials'}
					</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>

<ConfirmDialog
	open={pendingDelete !== null}
	title="Delete registry credential"
	message={`Delete the credential for ${pendingDelete?.registry ?? ''}? Deploys and pulls from it will fail until re-added. This cannot be undone.`}
	busy={false}
	onconfirm={deleteRegistry}
	onclose={() => (pendingDelete = null)}
/>
