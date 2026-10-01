<script lang="ts">
	import { api } from '$lib/api/client';
	import { containersBasePath } from '$lib/api/containers';
	import { addToast, selectedInstance, isOperator } from '$lib/store';
	import { routeParams } from '$lib/router';
	import type { ContainerDetail, ContainerInfo } from '$lib/types/generated';
	import { formatPorts } from '$lib/format';
	import Button from '../ui/Button.svelte';
	import Modal from '../ui/Modal.svelte';
	import ConfirmDialog from '../ui/ConfirmDialog.svelte';
	import StatsChart from '../Container/StatsChart.svelte';
	import LogsViewer from '../Container/LogsViewer.svelte';
	import ContainerTerminal from '../Container/ContainerTerminal.svelte';

	// container id from the /containers/:id route (see lib/router.ts)
	const containerId = $derived($routeParams.id ?? '');
	// detail container lives under the instance it runs on
	const instanceId = $derived($selectedInstance || 'local');
	// All reads/mutations target this instance's endpoints — the bare
	// /containers path always hits the local host (audit-stack-container C2).
	const basePath = $derived(containersBasePath(instanceId));

	let detail = $state<ContainerDetail | null>(null);
	let loading = $state(true);
	let error = $state('');
	let busy = $state<string | null>(null);
	let showDeleteDialog = $state(false);

	// edit modal
	let showEditModal = $state(false);
	let editName = $state('');
	let editRestartPolicy = $state('unless-stopped');
	let savingEdit = $state(false);

	// detail tabs: Overview / Logs / Env & Config / Terminal
	const tabs = ['overview', 'logs', 'env', 'terminal'] as const;
	type Tab = (typeof tabs)[number];
	let activeTab = $state<Tab>('overview');
	const isRunning = $derived((detail?.state ?? '').toLowerCase() === 'running');

	function formatBytes(bytes: number): string {
		if (bytes < 1024) return `${bytes} B`;
		if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
		if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
		return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`;
	}

	async function load() {
		if (!containerId) return;
		loading = true;
		error = '';
		try {
			detail = await api.get<ContainerInfo>(`${basePath}/${encodeURIComponent(containerId)}`);
			editName = detail.name ?? '';
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load container';
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		// Reload when the route id or the selected instance changes.
		void containerId;
		void basePath;
		load();
	});

	async function startContainer() {
		busy = 'start';
		try {
			await api.post(`${basePath}/${encodeURIComponent(containerId)}/start`);
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
			await api.post(`${basePath}/${encodeURIComponent(containerId)}/stop`);
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
			await api.post(`${basePath}/${encodeURIComponent(containerId)}/restart`);
			addToast('Container restarted', 'success');
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Restart failed', 'error');
		} finally {
			busy = null;
		}
	}

	async function deleteContainer() {
		busy = 'delete';
		try {
			await api.delete(`${basePath}/${encodeURIComponent(containerId)}?force=true`);
			addToast('Container deleted', 'success');
			// Redirect back to containers list
			window.history.back();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Delete failed', 'error');
		} finally {
			busy = null;
			showDeleteDialog = false;
		}
	}

	async function saveEdit() {
		if (!editName.trim()) {
			addToast('Container name required', 'error');
			return;
		}
		savingEdit = true;
		try {
			await api.put(`${basePath}/${encodeURIComponent(containerId)}`, {
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
				{#if $isOperator}
					<Button variant="secondary" size="sm" onclick={() => { showEditModal = true; }}>Edit</Button>
					<Button variant="primary" size="sm" disabled={busy === 'start' || detail.state?.toLowerCase() !== 'running'} onclick={startContainer}>Start</Button>
					<Button variant="secondary" size="sm" disabled={busy === 'stop'} onclick={stopContainer}>Stop</Button>
					<Button variant="danger" size="sm" disabled={busy === 'restart' || busy === 'delete'} onclick={restartContainer}>Restart</Button>
					<Button variant="danger" size="sm" disabled={busy === 'delete'} onclick={() => (showDeleteDialog = true)}>Delete</Button>
				{/if}
			</div>
		</header>

		<!-- Tabs -->
		<div class="flex gap-1 border-b border-zinc-800" role="tablist">
			{#each tabs as tab}
				<button
					role="tab"
					aria-selected={activeTab === tab}
					onclick={() => { activeTab = tab; }}
					class="px-3 py-2 text-sm transition-colors border-b-2 -mb-px"
					class:border-white={activeTab === tab}
					class:text-white={activeTab === tab}
					class:border-transparent={activeTab !== tab}
					class:text-zinc-500={activeTab !== tab}
					class:hover:text-zinc-300={activeTab !== tab}
				>
					{tab === 'overview' ? 'Overview' : tab === 'logs' ? 'Logs' : tab === 'env' ? 'Env & Config' : 'Terminal'}
				</button>
			{/each}
		</div>

		{#if activeTab === 'overview'}
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
						{#if detail.command && detail.command.length > 0}
							<div class="flex justify-between">
								<dt class="text-zinc-500">Command</dt>
								<dd class="text-zinc-300 font-mono break-all">{detail.command.join(' ')}</dd>
							</div>
						{/if}
						{#if detail.created}
							<div class="flex justify-between">
								<dt class="text-zinc-500">Created</dt>
								<dd class="text-zinc-300 font-mono">
									{new Date(detail.created * 1000).toLocaleString('en-US', { hour12: false })}
								</dd>
							</div>
						{/if}
						{#if detail.restart_policy}
							<div class="flex justify-between">
								<dt class="text-zinc-500">Restart policy</dt>
								<dd class="text-zinc-300 font-mono">{detail.restart_policy}</dd>
							</div>
						{/if}
						{#if detail.network_mode}
							<div class="flex justify-between">
								<dt class="text-zinc-500">Network mode</dt>
								<dd class="text-zinc-300 font-mono">{detail.network_mode}</dd>
							</div>
						{/if}
						{#if detail.memory_limit}
							<div class="flex justify-between">
								<dt class="text-zinc-500">Memory limit</dt>
								<dd class="text-zinc-300 font-mono">{formatBytes(detail.memory_limit)}</dd>
							</div>
						{/if}
					</dl>
				</section>

				<StatsChart containerId={containerId} />
			</div>
		{:else if activeTab === 'logs'}
			<LogsViewer {instanceId} {containerId} running={isRunning} />
		{:else if activeTab === 'env'}
			<div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
				<section class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
					<h3 class="text-sm font-semibold text-white mb-3">Environment ({detail.env?.length ?? 0})</h3>
					{#if detail.env && detail.env.length > 0}
						<ul role="list" class="space-y-1.5 max-h-96 overflow-y-auto">
							{#each detail.env as entry (entry)}
								<li class="px-2 py-1 rounded-sm bg-zinc-950 border border-zinc-800 text-xs font-mono text-zinc-300 break-all">{entry}</li>
							{/each}
						</ul>
					{:else}
						<p class="text-sm text-zinc-600">No environment variables</p>
					{/if}
				</section>

				<section class="bg-zinc-900 border border-zinc-800 rounded-sm p-4 space-y-4">
					<div>
						<h3 class="text-sm font-semibold text-white mb-2">Networks</h3>
						{#if detail.networks && Object.keys(detail.networks).length > 0}
							<ul role="list" class="space-y-1.5">
								{#each Object.entries(detail.networks) as [net, ip] (net)}
									<li class="flex justify-between text-sm">
										<span class="text-zinc-400 font-mono">{net}</span>
										<span class="text-zinc-300 font-mono">{ip || '—'}</span>
									</li>
								{/each}
							</ul>
						{:else}
							<p class="text-sm text-zinc-600">No networks</p>
						{/if}
					</div>

					<div>
						<h3 class="text-sm font-semibold text-white mb-2">Mounts ({detail.mounts?.length ?? 0})</h3>
						{#if detail.mounts && detail.mounts.length > 0}
							<ul role="list" class="space-y-1.5 max-h-64 overflow-y-auto">
								{#each detail.mounts as m (m.Source + m.Destination)}
									<li class="px-2 py-1.5 rounded-sm bg-zinc-950 border border-zinc-800 text-xs">
										<div class="flex items-center gap-2 mb-0.5">
											<span class="px-1.5 py-0.5 rounded bg-zinc-800 text-zinc-400 uppercase">{m.Type}</span>
											<span class={m.RW ? 'text-emerald-400' : 'text-amber-400'}>{m.RW ? 'RW' : 'RO'}</span>
										</div>
										<div class="font-mono text-zinc-300 break-all">{m.Source}</div>
										<div class="font-mono text-zinc-500 break-all">→ {m.Destination}</div>
									</li>
								{/each}
							</ul>
						{:else}
							<p class="text-sm text-zinc-600">No mounts</p>
						{/if}
					</div>
				</section>
			</div>
		{:else if activeTab === 'terminal'}
			<ContainerTerminal {instanceId} {containerId} running={isRunning} />
		{/if}
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

<ConfirmDialog
	open={showDeleteDialog}
	title="Delete container"
	message={`Delete container ${detail?.name ?? containerId}? This stops and removes the container. Its volumes are kept unless you remove them separately. This cannot be undone.`}
	busy={busy === 'delete'}
	onconfirm={deleteContainer}
	onclose={() => (showDeleteDialog = false)}
/>
