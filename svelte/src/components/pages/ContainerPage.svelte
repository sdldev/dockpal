<script lang="ts">
	import { api } from '$lib/api/client';
	import { addToast } from '$lib/store';
	import { routeParams } from '$lib/router';
	import type { ContainerDetail, ContainerInfo } from '$lib/types/generated';
	import { formatPorts } from '$lib/format';
	import Button from '../ui/Button.svelte';
	import Modal from '../ui/Modal.svelte';
	import StatsChart from '../Container/StatsChart.svelte';

	// container id from the /containers/:id route (see lib/router.ts)
	const containerId = $derived($routeParams.id ?? '');

	let detail = $state<ContainerDetail | null>(null);
	let loading = $state(true);
	let error = $state('');
	let busy = $state<string | null>(null);

	// edit modal
	let showEditModal = $state(false);
	let editName = $state('');
	let editRestartPolicy = $state('unless-stopped');
	let savingEdit = $state(false);

	async function load() {
		if (!containerId) return;
		loading = true;
		error = '';
		try {
			detail = await api.get<ContainerInfo>(`/containers/${containerId}`);
			editName = detail.name ?? '';
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load container';
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		load();
	});

	async function startContainer() {
		busy = 'start';
		try {
			await api.post(`/containers/${containerId}/start`);
			addToast('Container started', 'success');
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Start failed', 'error');
		} finally {
			busy = null;
		}
	}

	async function stopContainer() {
		busy = 'stop';
		try {
			await api.post(`/containers/${containerId}/stop`);
			addToast('Container stopped', 'success');
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Stop failed', 'error');
		} finally {
			busy = null;
		}
	}

	async function restartContainer() {
		busy = 'restart';
		try {
			await api.post(`/containers/${containerId}/restart`);
			addToast('Container restarted', 'success');
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Restart failed', 'error');
		} finally {
			busy = null;
		}
	}

	async function deleteContainer() {
		if (!confirm('Delete container? This cannot be undone.')) return;
		try {
			await api.delete(`/containers/${containerId}`);
			addToast('Container deleted', 'success');
			// Redirect back to containers list
			window.history.back();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Delete failed', 'error');
		}
	}

	async function saveEdit() {
		if (!editName.trim()) {
			addToast('Container name required', 'error');
			return;
		}
		savingEdit = true;
		try {
			await api.put(`/containers/${containerId}`, {
				name: editName.trim(),
				restart_policy: editRestartPolicy
			});
			addToast('Container updated', 'success');
			showEditModal = false;
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Update failed', 'error');
		} finally {
			savingEdit = false;
		}
	}

	function statusBadge(state: string) {
		switch (state.toLowerCase()) {
			case 'running': return 'bg-emerald-400/10 text-emerald-400';
			case 'exited': case 'stopped': return 'bg-zinc-400/10 text-zinc-400';
			case 'paused': return 'bg-amber-400/10 text-amber-400';
			case 'dead': return 'bg-red-400/10 text-red-400';
			default: return 'bg-blue-400/10 text-blue-400';
		}
	}
</script>

<div class="space-y-4">
	{#if !containerId}
		<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm">
			<p class="text-sm text-red-400">No container specified</p>
		</div>
	{/if}

	{#if error}
		<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm">
			<p class="text-sm text-red-400">{error}</p>
		</div>
	{/if}

	{#if loading}
		<div class="text-center py-12 text-zinc-500">Loading...</div>
	{/if}

	{#if detail && !loading}
		<header class="flex items-start justify-between">
			<div>
				<h2 class="text-lg font-semibold text-white">{detail.name}</h2>
				<p class="text-xs text-zinc-500 mt-1">ID: {containerId.slice(0, 12)}</p>
				<span class={`mt-2 inline-block px-2 py-0.5 rounded text-xs font-medium ${statusBadge(detail.state ?? 'unknown')}`}>
					{detail.state ?? 'unknown'}
				</span>
			</div>
			<div class="flex gap-2 shrink-0">
				<Button variant="secondary" size="sm" onclick={() => { showEditModal = true; }}>Edit</Button>
				<Button variant="primary" size="sm" disabled={busy === 'start' || detail.state?.toLowerCase() !== 'running'} onclick={startContainer}>Start</Button>
				<Button variant="secondary" size="sm" disabled={busy === 'stop'} onclick={stopContainer}>Stop</Button>
				<Button variant="danger" size="sm" disabled={busy === 'restart' || busy === 'delete'} onclick={restartContainer}>Restart</Button>
				<Button variant="danger" size="sm" disabled={busy === 'delete'} onclick={deleteContainer}>Delete</Button>
			</div>
		</header>

		<div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
			<section class="bg-zinc-900 border border-zinc-800 rounded-sm p-4 space-y-3">
				<h3 class="text-sm font-semibold text-white">Details</h3>
				<dl class="text-sm space-y-2">
					<div class="flex justify-between">
						<dt class="text-zinc-500">Image</dt>
						<dd class="text-zinc-300 font-mono">{detail.image}</dd>
					</div>
					{#if detail.ports && detail.ports.length > 0}
						<div class="flex justify-between">
							<dt class="text-zinc-500">Ports</dt>
							<dd class="text-zinc-300 font-mono">{formatPorts(detail.ports)}</dd>
						</div>
					{/if}
					{#if detail.command}
						<div class="flex justify-between">
							<dt class="text-zinc-500">Command</dt>
							<dd class="text-zinc-300 font-mono break-all">{detail.command}</dd>
						</div>
					{/if}
				</dl>
			</section>

			<StatsChart containerId={containerId} />
		</div>
	{/if}
</div>

<Modal
	open={showEditModal}
	title="Edit container"
	size="md"
	onclose={() => { showEditModal = false; }}
>
	<div class="space-y-3">
		<div>
			<label for="edit-name" class="block text-xs font-medium text-zinc-400 mb-1">Name</label>
			<input id="edit-name" type="text" bind:value={editName}
				class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white" />
		</div>
		<div>
			<label for="edit-restart" class="block text-xs font-medium text-zinc-400 mb-1">Restart policy</label>
			<select
				id="edit-restart"
				bind:value={editRestartPolicy}
				class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white"
			>
				<option value="no">no</option>
				<option value="always">always</option>
				<option value="unless-stopped">unless-stopped</option>
				<option value="on-failure">on-failure</option>
			</select>
		</div>
		<div class="flex justify-end gap-2 pt-2">
			<Button variant="secondary" disabled={savingEdit} onclick={() => { showEditModal = false; }}>Cancel</Button>
			<Button variant="primary" loading={savingEdit} onclick={saveEdit}>Save</Button>
		</div>
	</div>
</Modal>
