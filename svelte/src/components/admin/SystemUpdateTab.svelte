<script lang="ts">
	// System Update tab (Administration). Shows the running version, the latest
	// upstream release cached by the backend checker, and lets an admin trigger
	// an update. The update itself runs out-of-band (root systemd updater), so
	// after requesting it we poll the status endpoint — the panel restarts
	// mid-update, which we detect as a temporary outage followed by the new
	// version answering.
	import { getUpdateStatus, checkForUpdate, requestUpdate, type UpdateStatus } from '$lib/api/system';
	import { getHealthStatus } from '$lib/api/health';
	import { addToast } from '$lib/store';
	import Button from '../ui/Button.svelte';
	import Modal from '../ui/Modal.svelte';
	import ConfirmDialog from '../ui/ConfirmDialog.svelte';
	import Icon from '../ui/Icon.svelte';

	let status = $state<UpdateStatus | null>(null);
	let loading = $state(true);
	let checking = $state(false);
	let error = $state('');

	let showConfirm = $state(false);
	let updating = $state(false);
	// updatePhase drives the progress modal copy.
	let updatePhase = $state<'idle' | 'requested' | 'restarting' | 'done' | 'failed'>('idle');
	let updateMessage = $state('');
	let pollTimer: ReturnType<typeof setInterval> | null = null;

	async function load() {
		loading = true;
		error = '';
		try {
			status = await getUpdateStatus();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load update status';
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		load();
		return () => stopPolling();
	});

	async function checkNow() {
		checking = true;
		error = '';
		try {
			status = await checkForUpdate();
			if (status.update_available) {
				addToast(`Update available: v${status.latest_version}`, 'info');
			} else {
				addToast('Dockpal is up to date', 'success');
			}
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Check failed', 'error');
		} finally {
			checking = false;
		}
	}

	async function confirmUpdate() {
		if (!status?.latest_version) return;
		updating = true;
		try {
			await requestUpdate(status.latest_version);
			updatePhase = 'requested';
			updateMessage = `Update to v${status.latest_version} requested. The updater is starting…`;
			startPolling(status.latest_version);
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Update request failed', 'error');
		} finally {
			updating = false;
			showConfirm = false;
		}
	}

	function startPolling(target: string) {
		stopPolling();
		let unreachableStreak = 0;
		pollTimer = setInterval(async () => {
			// First: is the panel back up running the target version?
			try {
				const health = await getHealthStatus();
				unreachableStreak = 0;
				const running = (health.version || '').replace(/^v/, '');
				if (running === target) {
					finishUpdate('done', `Dockpal updated to v${target}.`);
					return;
				}
			} catch {
				// Panel is down — expected while it restarts mid-update.
				unreachableStreak++;
				if (updatePhase === 'requested' && unreachableStreak >= 1) {
					updatePhase = 'restarting';
					updateMessage = 'Server is restarting to apply the update…';
				}
			}

			// Also consult the status endpoint for a backend-recorded outcome
			// (covers the failure path where the old version keeps running).
			try {
				const s = await getUpdateStatus();
				if (s.state === 'failed') {
					finishUpdate('failed', s.state_detail.message || 'Update failed; rolled back.');
					return;
				}
				if (s.state === 'done') {
					finishUpdate('done', s.state_detail.message || `Dockpal updated to v${target}.`);
					return;
				}
			} catch {
				// Ignore — the health probe above is the primary restart signal.
			}
		}, 3000);
	}

	function finishUpdate(phase: 'done' | 'failed', message: string) {
		stopPolling();
		updatePhase = phase;
		updateMessage = message;
		addToast(message, phase === 'done' ? 'success' : 'error');
		load();
	}

	function stopPolling() {
		if (pollTimer) {
			clearInterval(pollTimer);
			pollTimer = null;
		}
	}

	function closeProgress() {
		stopPolling();
		updatePhase = 'idle';
		updateMessage = '';
	}

	function formatTime(unix?: number) {
		if (!unix) return 'never';
		return new Date(unix * 1000).toLocaleString();
	}
</script>

<div class="space-y-4">
	{#if error}
		<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm">
			<p class="text-sm text-red-400">{error}</p>
		</div>
	{/if}

	{#if loading && !status}
		<p class="text-sm text-zinc-500 text-center py-8">Loading update status...</p>
	{:else if status}
		<div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
			<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
				<div class="text-xs text-zinc-500 mb-1">Current version</div>
				<div class="text-sm text-white font-mono">v{status.current_version}</div>
			</div>
			<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
				<div class="text-xs text-zinc-500 mb-1">Latest version</div>
				<div class="text-sm font-mono" class:text-emerald-400={status.update_available} class:text-white={!status.update_available}>
					{status.latest_version ? `v${status.latest_version}` : 'unknown'}
				</div>
				<div class="text-xs text-zinc-500 mt-1">checked {formatTime(status.last_checked_at)}</div>
			</div>
		</div>

		{#if status.update_available && status.changelog}
			<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
				<div class="text-xs text-zinc-500 mb-2">What's new in v{status.latest_version}</div>
				<pre class="text-xs text-zinc-300 whitespace-pre-wrap font-sans max-h-48 overflow-y-auto">{status.changelog}</pre>
				{#if status.changelog_url}
					<a href={status.changelog_url} target="_blank" rel="noopener noreferrer" class="text-xs text-blue-400 hover:underline mt-2 inline-block">
						View full release notes →
					</a>
				{/if}
			</div>
		{/if}

		{#if !status.update_enabled}
			<div class="p-3 bg-amber-500/10 border border-amber-500/20 rounded-sm">
				<p class="text-sm text-amber-400">In-UI updates are disabled (DOCKPAL_UPDATE_ENABLED=0). Update from the host with update.sh.</p>
			</div>
		{/if}

		<div class="flex items-center gap-2">
			<Button variant="secondary" size="sm" loading={checking} onclick={checkNow}>
				<span class="inline-flex items-center gap-1.5"><Icon name="refresh" /> Check now</span>
			</Button>
			{#if status.update_available && status.update_enabled}
				<Button variant="primary" size="sm" onclick={() => (showConfirm = true)}>
					<span class="inline-flex items-center gap-1.5"><Icon name="download" /> Update to v{status.latest_version}</span>
				</Button>
			{:else if !status.update_available}
				<span class="text-sm text-zinc-500">Dockpal is up to date.</span>
			{/if}
		</div>
	{/if}
</div>

<ConfirmDialog
	open={showConfirm}
	title="Update Dockpal"
	message={`Update Dockpal from v${status?.current_version ?? ''} to v${status?.latest_version ?? ''}? The panel will restart briefly; running containers are not affected. The updater backs up the current binary and rolls back automatically if the new version fails its health check.`}
	confirmLabel="Update"
	busy={updating}
	onconfirm={confirmUpdate}
	onclose={() => (showConfirm = false)}
/>

<Modal open={updatePhase !== 'idle'} title="System update" size="sm" onclose={updatePhase === 'done' || updatePhase === 'failed' ? closeProgress : () => {}}>
	<div class="space-y-3">
		<div class="flex items-center gap-3">
			{#if updatePhase === 'done'}
				<span class="text-emerald-400 text-lg">✓</span>
			{:else if updatePhase === 'failed'}
				<span class="text-red-400 text-lg">✕</span>
			{:else}
				<span class="inline-block w-4 h-4 border-2 border-zinc-500 border-t-white rounded-full animate-spin"></span>
			{/if}
			<p class="text-sm text-zinc-200">{updateMessage}</p>
		</div>
		{#if updatePhase === 'requested' || updatePhase === 'restarting'}
			<p class="text-xs text-zinc-500">Do not close this tab's connection is fine to lose — the update continues on the host. This dialog updates automatically when the panel comes back.</p>
		{/if}
		{#if updatePhase === 'done' || updatePhase === 'failed'}
			<div class="flex justify-end">
				<Button variant="secondary" size="sm" onclick={closeProgress}>Close</Button>
			</div>
		{/if}
	</div>
</Modal>
