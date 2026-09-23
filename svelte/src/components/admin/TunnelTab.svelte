<script lang="ts">
	import { api } from '$lib/api/client';
	import { addToast } from '$lib/store';
	import Button from '../ui/Button.svelte';
	import ConfirmDialog from '../ui/ConfirmDialog.svelte';

	let token = $state('');
	let busy = $state(false);
	let tearing = $state(false);
	let showTeardownDialog = $state(false);

	async function setupTunnel() {
		if (!token.trim()) {
			addToast('Cloudflare tunnel token required', 'error');
			return;
		}
		busy = true;
		try {
			await api.post('/tunnel', { token: token.trim() });
			addToast('Tunnel deployed', 'success');
			token = '';
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Tunnel setup failed', 'error');
		} finally {
			busy = false;
		}
	}

	async function teardownTunnel() {
		tearing = true;
		try {
			await api.delete('/tunnel');
			addToast('Tunnel removed', 'success');
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Teardown failed', 'error');
		} finally {
			tearing = false;
			showTeardownDialog = false;
		}
	}
</script>

<div class="space-y-4">
	<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
		<h3 class="text-sm font-medium text-white mb-1">Cloudflare Tunnel</h3>
		<p class="text-xs text-zinc-500 mb-4">
			Deploy a cloudflared container with your tunnel token to expose Dockpal
			without opening inbound ports.
		</p>
		<form class="flex gap-2" onsubmit={(e) => { e.preventDefault(); setupTunnel(); }}>
			<input
				type="password"
				bind:value={token}
				placeholder="Tunnel token"
				class="flex-1 px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white"
			/>
			<Button type="submit" loading={busy}>Deploy tunnel</Button>
		</form>
	</div>

	<div class="bg-zinc-900 border border-red-500/20 rounded-sm p-4">
		<h3 class="text-sm font-medium text-red-400 mb-1">Danger zone</h3>
		<p class="text-xs text-zinc-500 mb-3">Stops and removes the cloudflared tunnel container.</p>
		<Button variant="danger" size="sm" loading={tearing} onclick={() => (showTeardownDialog = true)}>Tear down tunnel</Button>
	</div>
</div>

<ConfirmDialog
	open={showTeardownDialog}
	title="Tear down tunnel"
	message="Stop and remove the cloudflared tunnel container? Dockpal becomes unreachable via the tunnel domain until redeployed. This cannot be undone."
	confirmLabel="Tear down"
	busy={tearing}
	onconfirm={teardownTunnel}
	onclose={() => (showTeardownDialog = false)}
/>
