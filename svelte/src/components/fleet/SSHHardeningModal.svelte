<script lang="ts">
	// SSH hardening modal — per-server security detail + the hardening run.
	// Wiring (backend endpoints):
	//   POST /api/instances/:id/harden       → start the background job
	//   WS   /api/instances/:id/harden/logs  → live job log lines
	//
	// The job installs an SSH public key on the server, verifies key-only
	// login from a fresh connection, and ONLY THEN disables password auth in
	// sshd — with rollback on failure. It never runs unless the panel holds
	// stored SSH credentials for the instance (set during Add Server).
	import { onDestroy } from 'svelte';
	import { api, getToken } from '$lib/api/client';
	import { listSSHKeys, type SSHKeyInfo } from '$lib/api/sshkeys';
	import { addToast } from '$lib/store';
	import Modal from '../ui/Modal.svelte';
	import Button from '../ui/Button.svelte';
	import ConfirmDialog from '../ui/ConfirmDialog.svelte';
	import Icon from '../ui/Icon.svelte';

	interface Props {
		open: boolean;
		instance: {
			id: string;
			name: string;
			ssh_auth_type?: string;
			ssh_hardening_status?: string;
			ssh_hardened_at?: number;
		} | null;
		onclose: () => void;
		/** Called after a successful run so the caller can refresh badges. */
		onchanged?: () => void;
	}

	let { open, instance, onclose, onchanged }: Props = $props();

	type Phase = 'idle' | 'running' | 'done' | 'failed';
	let phase = $state<Phase>('idle');
	let logs = $state<string[]>([]);
	// Key to leave on the server: a fresh per-instance keypair (default — one
	// leaked key must not open other servers) or an existing saved key.
	let keySource = $state<'generate' | 'saved'>('generate');
	let savedKeys = $state<SSHKeyInfo[]>([]);
	let selectedKeyID = $state('');
	// The operator's own public key(s) — without one, their own PC loses shell
	// access the moment password auth is disabled (only the panel key remains).
	let extraPublicKeys = $state('');
	let confirmOpen = $state(false);
	let socket: WebSocket | null = null;

	$effect(() => {
		if (open) {
			phase = 'idle';
			logs = [];
			confirmOpen = false;
			extraPublicKeys = '';
			listSSHKeys()
				.then((keys) => (savedKeys = keys))
				.catch(() => (savedKeys = []));
		}
	});

	onDestroy(() => {
		socket?.close();
	});

	const displayName = $derived(instance ? (instance.id === 'local' ? 'This Server' : instance.name) : '');
	const isHardened = $derived(instance?.ssh_hardening_status === 'hardened');
	const authLabel = $derived(
		instance?.ssh_auth_type === 'key'
			? 'SSH key'
			: instance?.ssh_auth_type === 'password'
				? 'Password'
				: 'Unknown'
	);
	const canStart = $derived(
		phase === 'idle' && (keySource === 'generate' || selectedKeyID !== '')
	);

	function close() {
		socket?.close();
		socket = null;
		onclose();
	}

	async function start() {
		if (!instance || !canStart) return;
		confirmOpen = false;
		phase = 'running';
		logs = [];
		try {
			await api.post(`/instances/${instance.id}/harden`, {
				ssh_key_id: keySource === 'saved' ? selectedKeyID : undefined,
				extra_public_keys: extraPublicKeys
					.split('\n')
					.map((l) => l.trim())
					.filter(Boolean)
			});
			await openLogStream();
		} catch (e) {
			phase = 'failed';
			addToast(e instanceof Error ? e.message : 'Hardening failed to start', 'error');
		}
	}

	async function openLogStream() {
		const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
		// Single-use 60s ticket instead of the 4h JWT in the URL (audit L1).
		let credential = getToken() ?? '';
		try {
			const res = await api.get<{ ticket: string }>('/ws-ticket');
			credential = res.ticket;
		} catch {
			// Older backend without /ws-ticket — fall back to the JWT.
		}
		socket = new WebSocket(
			`${proto}//${location.host}/api/instances/${instance?.id}/harden/logs?token=${credential}`
		);
		socket.onmessage = (event) => {
			const line = String(event.data);
			logs = [...logs, line];
			// The job's final lines are deterministic markers written after the
			// instance record was updated (success) or on any failure.
			if (line.includes('[Dockpal Hardening] Error:')) {
				phase = 'failed';
				socket?.close();
			} else if (line.includes('[Dockpal Hardening] Hardening completed successfully')) {
				phase = 'done';
				addToast(`"${displayName}" is hardened — password login disabled`, 'success');
				onchanged?.();
				socket?.close();
			}
		};
		socket.onclose = () => {
			if (phase === 'running') phase = 'failed';
		};
	}
</script>

<Modal {open} title={`SSH hardening — ${displayName}`} size="lg" onclose={close}>
	{#if instance}
		<div class="space-y-4">
			<!-- Current security state -->
			<div class="bg-zinc-950 border border-zinc-800/60 rounded-sm p-4">
				<h4 class="text-xs font-semibold text-zinc-400 uppercase tracking-wider mb-2">Current state</h4>
				<div class="flex items-center gap-2 text-sm">
					{#if isHardened}
						<span class="inline-flex items-center gap-1.5 font-semibold px-2 py-0.5 rounded bg-green-500/10 text-green-400">
							<Icon name="admin" class="w-3.5 h-3.5" /> Hardened
						</span>
						<span class="text-xs text-zinc-500">
							key-only auth since {instance.ssh_hardened_at
								? new Date(instance.ssh_hardened_at * 1000).toLocaleString()
								: '—'}
						</span>
					{:else}
						<span
							class={`inline-flex items-center gap-1.5 font-semibold px-2 py-0.5 rounded ${instance.ssh_auth_type === 'password' ? 'bg-amber-500/10 text-amber-400' : 'bg-blue-500/10 text-blue-400'}`}
						>
							<Icon name="admin" class="w-3.5 h-3.5" /> {authLabel} login
						</span>
						<span class="text-xs text-zinc-500">password authentication is still active on the server</span>
					{/if}
				</div>
			</div>

			<!-- What hardening does -->
			<div class="text-sm text-zinc-400 space-y-2">
				<p>Hardening switches this server to SSH-key-only login:</p>
				<ol class="list-decimal list-inside space-y-1 text-zinc-500 text-xs pl-1">
					<li>Install a public key into <span class="font-mono text-zinc-400">~/.ssh/authorized_keys</span></li>
					<li>Verify key-only login works in a fresh connection</li>
					<li>Disable <span class="font-mono text-zinc-400">PasswordAuthentication</span> in sshd (drop-in + <span class="font-mono text-zinc-400">sshd -t</span> + reload)</li>
					<li>Verify from outside: key accepted, password rejected</li>
				</ol>
				<p class="text-amber-400/90 text-xs">
					⚠ Make sure you keep another way in (cloud provider console/VNC) before disabling
					password login. On success, Dockpal removes the stored password and uses the key.
				</p>
			</div>

			<!-- Key choice -->
			<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
				<div>
					<label for="harden-key-source" class="block text-xs font-medium text-zinc-400 mb-1">Key to install</label>
					<select
						id="harden-key-source"
						bind:value={keySource}
						disabled={phase !== 'idle'}
						class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
					>
						<option value="generate">Generate dedicated key (recommended)</option>
						<option value="saved">Saved key</option>
					</select>
				</div>
				{#if keySource === 'saved'}
					<div>
						<label for="harden-saved-key" class="block text-xs font-medium text-zinc-400 mb-1">Saved key</label>
						<select
							id="harden-saved-key"
							bind:value={selectedKeyID}
							disabled={phase !== 'idle'}
							class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
						>
							{#each savedKeys as k (k.id)}
								<option value={k.id}>{k.name} — {k.key_type} {k.fingerprint.slice(0, 20)}…</option>
							{:else}
								<option value="">No saved keys — Settings → Administration → SSH Keys</option>
							{/each}
						</select>
					</div>
				{/if}
			</div>

			<!-- Operator's own key: keeps their PC able to log in after hardening -->
			<div>
				<label for="harden-extra-keys" class="block text-xs font-medium text-zinc-400 mb-1">
					Your public key <span class="text-zinc-600">(recommended)</span>
				</label>
				<textarea
					id="harden-extra-keys"
					bind:value={extraPublicKeys}
					disabled={phase !== 'idle'}
					rows="2"
					placeholder="ssh-ed25519 AAAA... you@your-pc"
					class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-xs text-white font-mono focus:outline-none focus:ring-2 focus:ring-blue-600"
				></textarea>
				<p class="text-xs text-zinc-600 mt-1">
					Paste the output of <span class="font-mono text-zinc-400">cat ~/.ssh/id_ed25519.pub</span> from
					your own machine — without it, only Dockpal can log in after passwords are disabled.
				</p>
			</div>

			<!-- Run log -->
			{#if logs.length > 0}
				<div>
					<h4 class="text-xs font-semibold text-zinc-400 uppercase tracking-wider mb-1.5">Hardening log</h4>
					<pre class="p-3 bg-black border border-zinc-800 rounded-sm text-xs text-zinc-300 font-mono whitespace-pre-wrap overflow-auto max-h-56">{logs.join('\n')}</pre>
				</div>
			{/if}

			{#if phase === 'done'}
				<div class="p-3 bg-green-500/10 border border-green-500/20 rounded-sm text-sm text-green-400">
					Hardening complete — the server now only accepts the installed SSH key. Dockpal stores
					its private key encrypted and no longer keeps the password.
				</div>
			{:else if phase === 'failed'}
				<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm text-sm text-red-400">
					Hardening failed — the server was left unchanged (the key may have been installed, which
					is safe). Review the log and retry.
				</div>
			{/if}

			<div class="flex justify-end gap-2 pt-1">
				<Button variant="secondary" size="sm" onclick={close}>Close</Button>
				{#if phase !== 'running'}
					<Button variant="primary" size="sm" disabled={!canStart} onclick={() => (confirmOpen = true)}>
						{isHardened ? 'Re-run hardening' : 'Install key & disable password login'}
					</Button>
				{:else}
					<Button variant="primary" size="sm" loading={true}>Hardening…</Button>
				{/if}
			</div>
		</div>
	{/if}
</Modal>

<ConfirmDialog
	open={confirmOpen}
	title="Disable password login"
	message={`Install the SSH key on "${displayName}" and disable password authentication in sshd? Key-only login is verified BEFORE passwords are turned off, and everything rolls back automatically on failure.${extraPublicKeys.trim() ? ' Your own public key will also be installed so you keep CLI access.' : ' Without your own public key, only Dockpal will be able to log in.'} Keep in mind you should still have access to the provider console as a last resort.`}
	confirmLabel="Harden server"
	busy={phase === 'running'}
	onconfirm={start}
	onclose={() => (confirmOpen = false)}
/>
