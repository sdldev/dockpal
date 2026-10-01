<script lang="ts">
	// Self-contained system-update flow (confirm → progress → result), rendered
	// from the sidebar footer. The backend stages a trigger file; the root
	// systemd updater swaps the binary and restarts the panel, so the progress
	// phase polls across the restart (health probe first, status endpoint for
	// the backend-recorded outcome).
	import { requestUpdate, getUpdateStatus } from '$lib/api/system';
	import { getHealthStatus } from '$lib/api/health';
	import { addToast } from '$lib/store';
	import Button from '../ui/Button.svelte';
	import Modal from '../ui/Modal.svelte';
	import ConfirmDialog from '../ui/ConfirmDialog.svelte';

	interface Props {
		open: boolean;
		// Version to install (e.g. "1.0.1"); empty means "up to date" — the
		// component renders nothing.
		target: string;
		current: string;
		onfinished?: (ok: boolean) => void;
		onclose: () => void;
	}
	let { open, target, current, onfinished, onclose }: Props = $props();

	// phase drives the progress modal; 'confirm' shows the confirmation dialog.
	let phase = $state<'confirm' | 'requested' | 'restarting' | 'done' | 'failed'>('confirm');
	let busy = $state(false);
	let message = $state('');
	let pollTimer: ReturnType<typeof setInterval> | null = null;

	function stopPolling() {
		if (pollTimer) {
			clearInterval(pollTimer);
			pollTimer = null;
		}
	}

	// Reset when the dialog is (re)opened.
	$effect(() => {
		if (open) {
			stopPolling();
			phase = 'confirm';
			message = '';
		}
		return () => stopPolling();
	});

	async function confirmUpdate() {
		if (!target) return;
		busy = true;
		try {
			await requestUpdate(target);
			phase = 'requested';
			message = `Update to v${target} requested. The updater is starting…`;
			startPolling();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Update request failed', 'error');
		} finally {
			busy = false;
		}
	}

	function startPolling() {
		stopPolling();
		pollTimer = setInterval(async () => {
			// Primary signal: is the panel back up running the target version?
			try {
				const health = await getHealthStatus();
				const running = (health.version || '').replace(/^v/, '');
				if (running === target) {
					finish(true, `Dockpal updated to v${target}.`);
					return;
				}
			} catch {
				// Panel down — expected while it restarts mid-update.
				if (phase === 'requested') {
					phase = 'restarting';
					message = 'Server is restarting to apply the update…';
				}
			}

			// Secondary: the backend-recorded outcome (covers the failure path
			// where the old version keeps running after a rollback).
			try {
				const s = await getUpdateStatus();
				if (s.state === 'failed') {
					finish(false, s.state_detail.message || 'Update failed; rolled back.');
				} else if (s.state === 'done') {
					finish(true, s.state_detail.message || `Dockpal updated to v${target}.`);
				}
			} catch {
				// Ignore — the health probe above is the primary signal.
			}
		}, 3000);
	}

	function finish(ok: boolean, msg: string) {
		stopPolling();
		phase = ok ? 'done' : 'failed';
		message = msg;
		addToast(msg, ok ? 'success' : 'error');
		onfinished?.(ok);
	}

	function close() {
		stopPolling();
		onclose();
	}
</script>

<ConfirmDialog
	open={open && phase === 'confirm'}
	title="Update Dockpal"
	message={`Update Dockpal from v${current} to v${target}? The panel will restart briefly; running containers are not affected. The updater backs up the current binary and rolls back automatically if the new version fails its health check.`}
	confirmLabel="Update"
	busy={busy}
	onconfirm={confirmUpdate}
	onclose={onclose}
/>

<Modal
	open={open && phase !== 'confirm'}
	title="System update"
	size="sm"
	onclose={phase === 'done' || phase === 'failed' ? close : () => {}}
>
	<div class="space-y-3">
		<div class="flex items-center gap-3">
			{#if phase === 'done'}
				<span class="text-emerald-400 text-lg">✓</span>
			{:else if phase === 'failed'}
				<span class="text-red-400 text-lg">✕</span>
			{:else}
				<span class="inline-block w-4 h-4 border-2 border-zinc-500 border-t-white rounded-full animate-spin"></span>
			{/if}
			<p class="text-sm text-zinc-200">{message}</p>
		</div>
		{#if phase === 'requested' || phase === 'restarting'}
			<p class="text-xs text-zinc-500">
				The update continues on the host even if this page is closed. This dialog updates automatically when the panel comes back.
			</p>
		{/if}
		{#if phase === 'done' || phase === 'failed'}
			<div class="flex justify-end">
				<Button variant="secondary" size="sm" onclick={close}>Close</Button>
			</div>
		{/if}
	</div>
</Modal>
