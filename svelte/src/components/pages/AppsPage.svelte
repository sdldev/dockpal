<script lang="ts">
	import { api } from '$lib/api/client';
	import { addToast } from '$lib/store';
	import type { AppSummary, AppUpdateRecord } from '$lib/types/generated';
	import type { Service } from '$lib/types/api';
	import Button from '../ui/Button.svelte';
	import Modal from '../ui/Modal.svelte';

	let apps = $state<AppSummary[]>([]);
	let loading = $state(true);
	let error = $state('');
	let busy = $state<string | null>(null);
	let historyApp = $state<string | null>(null);
	let history = $state<AppUpdateRecord[]>([]);
	let historyLoading = $state(false);

	// Uninstall
	let uninstallTarget = $state<string | null>(null);
	let uninstalling = $state(false);

	async function load() {
		loading = true;
		error = '';
		try {
			apps = await api.get<AppSummary[]>('/apps');
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load apps';
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		load();
	});

	const stats = $derived.by(() => {
		let running = 0;
		let stopped = 0;
		let updates = 0;
		for (const app of apps) {
			if (app.services.length > 0 && app.services.every((s) => s.state === 'running')) running++;
			if (app.services.some((s) => s.state && s.state !== 'running')) stopped++;
			if (app.has_update) updates++;
		}
		return { total: apps.length, running, stopped, updates };
	});

	async function triggerUpdate(name: string) {
		busy = name;
		try {
			const resp = await api.post<{ attempt_id?: string; status?: string }>(`/apps/${name}/update`);
			addToast(resp.attempt_id ? `Update started (attempt ${resp.attempt_id.slice(0, 8)}…)` : 'Update request accepted', 'success');
			// Give the worker a moment to persist the first record
			setTimeout(() => load(), 1500);
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Update failed', 'error');
		} finally {
			busy = null;
		}
	}

	async function toggleAutoUpdate(name: string, enabled: boolean) {
		busy = name;
		try {
			await api.patch(`/apps/${name}/auto-update`, { enabled });
			addToast(`Auto-update ${enabled ? 'enabled' : 'disabled'} for ${name}`, 'success');
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Toggle failed', 'error');
		} finally {
			busy = null;
		}
	}

	async function openHistory(name: string) {
		historyApp = name;
		historyLoading = true;
		history = [];
		try {
			history = await api.get<AppUpdateRecord[]>(`/apps/${name}/updates`);
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Failed to load history', 'error');
		} finally {
			historyLoading = false;
		}
	}

	async function uninstall(name: string) {
		uninstalling = true;
		try {
			const services = await api.get<Service[]>('/services');
			const service = services.find((s) => s.name === name);
			if (!service) {
				addToast('Service record not found; containers may need manual removal', 'error');
				uninstallTarget = null;
				return;
			}
			await api.delete(`/services/${service.id}`);
			addToast(`${name} uninstalled`, 'success');
			uninstallTarget = null;
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Uninstall failed', 'error');
		} finally {
			uninstalling = false;
		}
	}

	function stateLabel(state: string | undefined) {
		if (!state) return 'unknown';
		return state;
	}

	function formatTime(ts: number) {
		return new Date(ts * 1000).toLocaleString('en-US', { hour12: false });
	}

	function stageClass(stage: string) {
		if (stage === 'completed' || stage === 'done') return 'bg-emerald-400/10 text-emerald-400';
		if (stage === 'failed' || stage === 'error') return 'bg-red-400/10 text-red-400';
		return 'bg-blue-400/10 text-blue-400';
	}
</script>

<div class="space-y-4">
	<div>
		<h2 class="text-lg font-semibold text-white">Installed Apps</h2>
		<p class="text-sm text-zinc-500">Compose projects deployed through the panel</p>
	</div>

	<!-- Summary cards -->
	<div class="grid grid-cols-2 lg:grid-cols-4 gap-3">
		<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
			<p class="text-2xl font-bold text-white">{stats.total}</p>
			<p class="text-xs text-zinc-500 mt-1">Total apps</p>
		</div>
		<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
			<p class="text-2xl font-bold text-emerald-400">{stats.running}</p>
			<p class="text-xs text-zinc-500 mt-1">Running</p>
		</div>
		<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
			<p class="text-2xl font-bold text-red-400">{stats.stopped}</p>
			<p class="text-xs text-zinc-500 mt-1">Stopped / degraded</p>
		</div>
		<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
			<p class="text-2xl font-bold text-amber-400">{stats.updates}</p>
			<p class="text-xs text-zinc-500 mt-1">Updates available</p>
		</div>
	</div>

	{#if error}
		<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm">
			<p class="text-sm text-red-400">{error}</p>
		</div>
	{/if}

	<div class="space-y-3">
		{#each apps as app (app.name)}
			<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
				<div class="flex items-start justify-between gap-4">
					<div>
						<div class="flex items-center gap-2">
							<h3 class="text-sm font-semibold text-white">{app.name}</h3>
							{#if app.has_update}
								<span class="px-2 py-0.5 rounded text-xs font-medium bg-amber-400/10 text-amber-400">update available</span>
							{/if}
							{#if app.auto_update}
								<span class="px-2 py-0.5 rounded text-xs font-medium bg-emerald-400/10 text-emerald-400">auto-update on</span>
							{/if}
						</div>
						<p class="text-xs text-zinc-500 mt-1">{app.services.length} service(s)</p>
						{#if app.last_update}
							<p class="text-xs text-zinc-600 mt-1">
								Last update: {formatTime(app.last_update.started_at)} — <span class="capitalize">{app.last_update.stage}</span>
							</p>
						{/if}
					</div>
					<div class="flex gap-2 shrink-0">
						<Button variant="secondary" size="sm" onclick={() => openHistory(app.name)}>History</Button>
						<Button
							variant="secondary"
							size="sm"
							disabled={busy === app.name}
							onclick={() => toggleAutoUpdate(app.name, !app.auto_update)}
						>
							{app.auto_update ? 'Disable auto' : 'Enable auto'}
						</Button>
						<Button
							variant="primary"
							size="sm"
							loading={busy === app.name}
							onclick={() => triggerUpdate(app.name)}
						>
							Update now
						</Button>
						<Button
							variant="danger"
							size="sm"
							onclick={() => { uninstallTarget = app.name; }}
						>
							Uninstall
						</Button>
					</div>
				</div>
				<div class="mt-3 flex flex-wrap gap-2">
					{#each app.services as svc (svc.name)}
						<span class="px-2 py-1 rounded text-xs bg-zinc-950 border border-zinc-800 font-mono text-zinc-400 inline-flex items-center gap-1.5">
							<span class="w-1.5 h-1.5 rounded-full shrink-0" class:bg-emerald-400={svc.state === 'running'} class:bg-red-400={svc.state && svc.state !== 'running'} class:bg-zinc-600={!svc.state}></span>
							{svc.image}
							{#if svc.has_update}
								<span class="text-amber-400">•</span>
							{/if}
							<span class="text-[10px] uppercase {svc.state === 'running' ? 'text-emerald-400' : svc.state ? 'text-red-400' : 'text-zinc-600'}">{stateLabel(svc.state)}</span>
						</span>
					{/each}
				</div>
			</div>
		{:else}
			<div class="text-center py-12 text-zinc-600 text-sm bg-zinc-900 border border-zinc-800 rounded-sm">
				{loading ? 'Loading apps...' : 'No installed apps yet — deploy from the App Installer'}
			</div>
		{/each}
	</div>
</div>

<Modal open={historyApp !== null} title={`Update history — ${historyApp ?? ''}`} size="lg" onclose={() => { historyApp = null; }}>
	{#if historyLoading}
		<p class="text-sm text-zinc-500 text-center py-6">Loading history...</p>
	{:else if history.length === 0}
		<p class="text-sm text-zinc-600 text-center py-6">No update attempts yet</p>
	{:else}
		<div class="space-y-2 max-h-96 overflow-y-auto">
			{#each history as rec (rec.attempt_id)}
				<div class="bg-zinc-950 border border-zinc-800 rounded-sm p-3">
					<div class="flex items-center justify-between mb-1">
						<span class="px-2 py-0.5 rounded text-xs font-medium {stageClass(rec.stage)}">{rec.stage}</span>
						<span class="text-xs text-zinc-600">{formatTime(rec.started_at)}</span>
					</div>
					<p class="text-xs text-zinc-500">by {rec.triggered_by}</p>
					{#if rec.message}
						<p class="text-xs text-zinc-400 mt-1 font-mono">{rec.message}</p>
					{/if}
					{#if rec.error_code}
						<p class="text-xs text-red-400 mt-1 font-mono">{rec.error_code}</p>
					{/if}
				</div>
			{/each}
		</div>
	{/if}
</Modal>

<Modal open={uninstallTarget !== null} title={`Uninstall ${uninstallTarget ?? ''}`} size="sm" onclose={() => { if (!uninstalling) uninstallTarget = null; }}>
	<p class="text-sm text-zinc-400">Remove this app and all of its containers? The compose project will be deleted and cannot be recovered.</p>
	<div class="flex gap-2 mt-4 justify-end">
		<Button variant="secondary" onclick={() => { uninstallTarget = null; }} disabled={uninstalling}>Cancel</Button>
		<Button variant="danger" loading={uninstalling} onclick={() => uninstallTarget && uninstall(uninstallTarget)}>Uninstall</Button>
	</div>
</Modal>