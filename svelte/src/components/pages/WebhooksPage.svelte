<script lang="ts">
	import { api } from '$lib/api/client';
	import { addToast } from '$lib/store';
	import type { Webhook } from '$lib/types/generated';
	import Button from '../ui/Button.svelte';
	import ConfirmDialog from '../ui/ConfirmDialog.svelte';

	let webhooks = $state<Webhook[]>([]);
	let loading = $state(true);
	let error = $state('');
	let busy = $state(false);
	let pendingDelete = $state<Webhook | null>(null);

	// create form
	let showForm = $state(false);
	let name = $state('');
	let repo = $state('');
	let branch = $state('');
	let composeFile = $state('');
	let secret = $state('');

	async function load() {
		loading = true;
		error = '';
		try {
			webhooks = await api.get<Webhook[]>('/webhooks');
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load webhooks';
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		load();
	});

	async function createWebhook() {
		if (!name.trim() || !repo.trim()) {
			addToast('Name and repo are required', 'error');
			return;
		}
		busy = true;
		try {
			await api.post('/webhooks', {
				instance_id: 'local',
				name: name.trim(),
				repo: repo.trim(),
				branch: branch.trim() || undefined,
				compose_file: composeFile.trim() || undefined,
				secret: secret.trim() || undefined
			});
			addToast('Webhook created', 'success');
			name = '';
			repo = '';
			branch = '';
			composeFile = '';
			secret = '';
			showForm = false;
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Create failed', 'error');
		} finally {
			busy = false;
		}
	}

	async function deleteWebhook() {
		const target = pendingDelete;
		if (!target) return;
		try {
			await api.delete(`/webhooks/${target.id}`);
			addToast('Webhook deleted', 'success');
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Delete failed', 'error');
		} finally {
			pendingDelete = null;
		}
	}

	async function copyDeployUrl(id: string) {
		const url = `${location.origin}/api/webhooks/deploy/${id}`;
		try {
			await navigator.clipboard.writeText(url);
			addToast('Deploy URL copied', 'info');
		} catch {
			addToast('Clipboard unavailable — copy manually', 'error');
		}
	}

	function formatTime(ts: number) {
		return new Date(ts * 1000).toLocaleString('en-US', { hour12: false });
	}
</script>

<div class="space-y-4">
	<div class="flex items-center justify-between">
		<div>
			<h2 class="text-lg font-semibold text-white">Webhooks</h2>
			<p class="text-sm text-zinc-500">Git push-to-deploy triggers (no auth on the deploy URL — use a secret)</p>
		</div>
		<Button variant="primary" size="sm" onclick={() => { showForm = !showForm; }}>
			{showForm ? 'Cancel' : 'New webhook'}
		</Button>
	</div>

	{#if showForm}
		<form class="bg-zinc-900 border border-zinc-800 rounded-sm p-4 grid grid-cols-1 sm:grid-cols-2 gap-3" onsubmit={(e) => { e.preventDefault(); createWebhook(); }}>
			<div>
				<label for="wh-name" class="block text-xs font-medium text-zinc-400 mb-1">Name</label>
				<input id="wh-name" type="text" bind:value={name} placeholder="my-app"
					class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white" />
			</div>
			<div>
				<label for="wh-repo" class="block text-xs font-medium text-zinc-400 mb-1">Git repo</label>
				<input id="wh-repo" type="text" bind:value={repo} placeholder="owner/repo"
					class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white" />
			</div>
			<div>
				<label for="wh-branch" class="block text-xs font-medium text-zinc-400 mb-1">Branch (optional)</label>
				<input id="wh-branch" type="text" bind:value={branch} placeholder="main"
					class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white" />
			</div>
			<div>
				<label for="wh-compose" class="block text-xs font-medium text-zinc-400 mb-1">Compose file (optional)</label>
				<input id="wh-compose" type="text" bind:value={composeFile} placeholder="docker-compose.yml"
					class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white" />
			</div>
			<div class="sm:col-span-2">
				<label for="wh-secret" class="block text-xs font-medium text-zinc-400 mb-1">Secret (optional, HMAC signature check)</label>
				<input id="wh-secret" type="password" bind:value={secret} placeholder="shared secret"
					class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white" />
			</div>
			<div class="sm:col-span-2 flex justify-end">
				<Button type="submit" loading={busy}>Create webhook</Button>
			</div>
		</form>
	{/if}

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
					<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Repo</th>
					<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Branch</th>
					<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Secret</th>
					<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Created</th>
					<th class="text-right px-4 py-2.5 text-xs font-medium text-zinc-500">Actions</th>
				</tr>
			</thead>
			<tbody>
				{#each webhooks as wh (wh.id)}
					<tr class="border-b border-zinc-800/50 hover:bg-zinc-800/20 transition-colors">
						<td class="px-4 py-2.5 text-sm text-white">{wh.name}</td>
						<td class="px-4 py-2.5 text-sm text-zinc-400 font-mono">{wh.repo}</td>
						<td class="px-4 py-2.5 text-sm text-zinc-500">{wh.branch || '—'}</td>
						<td class="px-4 py-2.5">
							{#if wh.has_secret}
								<span class="px-2 py-0.5 rounded text-xs font-medium bg-emerald-400/10 text-emerald-400">protected</span>
							{:else}
								<span class="px-2 py-0.5 rounded text-xs font-medium bg-amber-400/10 text-amber-400">open</span>
							{/if}
						</td>
						<td class="px-4 py-2.5 text-xs text-zinc-500">{formatTime(wh.created_at)}</td>
						<td class="px-4 py-2.5 text-right space-x-2">
							<Button variant="secondary" size="sm" onclick={() => copyDeployUrl(wh.id)}>Copy URL</Button>
							<Button variant="danger" size="sm" onclick={() => (pendingDelete = wh)}>Delete</Button>
						</td>
					</tr>
				{:else}
					<tr>
						<td colspan="6" class="text-center py-8 text-zinc-600 text-sm">
							{loading ? 'Loading webhooks...' : 'No webhooks'}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>

	<ConfirmDialog
		open={pendingDelete !== null}
		title="Delete webhook"
		message={`Delete webhook ${pendingDelete?.name ?? ''}? Its deploy URL stops working immediately. This cannot be undone.`}
		busy={false}
		onconfirm={deleteWebhook}
		onclose={() => (pendingDelete = null)}
	/>
</div>
