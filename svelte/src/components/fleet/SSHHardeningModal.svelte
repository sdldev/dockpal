<script lang="ts">
	// Server security modal — per-server security detail, live detection and
	// controls, plus the guided SSH-hardening run.
	// Wiring (backend endpoints):
	//   GET  /api/instances/:id/security      → live detection (sshd -T, fail2ban)
	//   POST /api/instances/:id/security      → apply one control (background job)
	//   WS   /api/instances/:id/security/logs → live job log lines
	//   POST /api/instances/:id/harden        → guided hardening job (key + disable pw)
	//   WS   /api/instances/:id/harden/logs   → hardening job log lines
	//
	// Jobs only run when the panel holds stored SSH credentials for the
	// instance (set during Add Server). Everything is fail-closed: an unknown
	// state is displayed as NOT secured.
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
			sec_password_auth?: string;
			sec_root_login?: string;
			sec_fail2ban?: string;
			sec_checked_at?: number;
		} | null;
		onclose: () => void;
		/** Called after successful runs so the caller can refresh badges. */
		onchanged?: () => void;
	}

	let { open, instance, onclose, onchanged }: Props = $props();

	type Phase = 'idle' | 'running' | 'done' | 'failed';
	interface SecState {
		password_auth: string;
		root_login: string;
		fail2ban: string;
	}

	let phase = $state<Phase>('idle'); // hardening job
	let secJob = $state<Phase>('idle'); // security control job
	let logs = $state<string[]>([]); // shared log area (latest job)
	let logSource = $state<'harden' | 'security'>('harden');
	// Key to leave on the server: a fresh per-instance keypair (default — one
	// leaked key must not open other servers) or an existing saved key.
	let keySource = $state<'generate' | 'saved'>('generate');
	let savedKeys = $state<SSHKeyInfo[]>([]);
	let selectedKeyID = $state('');
	// The operator's own public key(s) — without one, their own PC loses shell
	// access the moment password auth is disabled (only the panel key remains).
	let extraPublicKeys = $state('');
	let selectedExtraKeyIDs = $state<string[]>([]);
	let confirmOpen = $state(false);
	let socket: WebSocket | null = null;
	// Live detected state.
	let secState = $state<SecState | null>(null);
	let secError = $state('');
	let secChecking = $state(false);
	// Pending control toggle awaiting confirmation.
	let pendingControl = $state<{ control: string; enabled: boolean } | null>(null);

	$effect(() => {
		if (open) {
			phase = 'idle';
			secJob = 'idle';
			logs = [];
			confirmOpen = false;
			extraPublicKeys = '';
			selectedExtraKeyIDs = [];
			secState = instance
				? {
						password_auth: instance.sec_password_auth ?? '',
						root_login: instance.sec_root_login ?? '',
						fail2ban: instance.sec_fail2ban ?? ''
					}
				: null;
			listSSHKeys()
				.then((keys) => {
					savedKeys = keys.filter((k) => k.secret_type === 'public');
				})
				.catch(() => (savedKeys = []));
			detect();
		}
	});

	onDestroy(() => {
		socket?.close();
	});

	async function detect() {
		if (!instance || instance.id === 'local') return;
		secChecking = true;
		secError = '';
		try {
			const res = await api.get<{ security: SecState; error?: string }>(
				`/instances/${instance.id}/security`
			);
			secState = res.security;
			secError = res.error ?? '';
		} catch (e) {
			secError = e instanceof Error ? e.message : 'Security check failed';
		} finally {
			secChecking = false;
		}
	}

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
		phase === 'idle' && secJob === 'idle' && (keySource === 'generate' || selectedKeyID !== '')
	);
	const busy = $derived(phase === 'running' || secJob === 'running');

	function pretty(v: string): string {
		if (!v) return '—';
		if (v === 'unknown') return 'Unknown';
		if (v === 'prohibit-password' || v === 'without-password') return 'Keys only';
		if (v === 'inactive') return 'Inactive';
		if (v === 'active') return 'Active';
		return v === 'yes' ? 'On' : v === 'no' ? 'Off' : v;
	}

	function close() {
		socket?.close();
		socket = null;
		onclose();
	}

	function toggleExtraKey(id: string) {
		selectedExtraKeyIDs = selectedExtraKeyIDs.includes(id)
			? selectedExtraKeyIDs.filter((x) => x !== id)
			: [...selectedExtraKeyIDs, id];
	}

	// ---- guided hardening run ----
	async function start() {
		if (!instance || !canStart) return;
		confirmOpen = false;
		phase = 'running';
		logSource = 'harden';
		logs = [];
		try {
			const savedLines = selectedExtraKeyIDs
				.map((id) => savedKeys.find((k) => k.id === id)?.public_key?.trim())
				.filter((l): l is string => !!l);
			const pastedLines = extraPublicKeys
				.split('\n')
				.map((l) => l.trim())
				.filter(Boolean);
			await api.post(`/instances/${instance.id}/harden`, {
				ssh_key_id: keySource === 'saved' ? selectedKeyID : undefined,
				extra_public_keys: [...savedLines, ...pastedLines]
			});
			await openLogStream('harden');
		} catch (e) {
			phase = 'failed';
			addToast(e instanceof Error ? e.message : 'Hardening failed to start', 'error');
		}
	}

	// ---- security controls ----
	function askControl(control: string, enabled: boolean) {
		pendingControl = { control, enabled };
	}

	async function startControl() {
		if (!instance || !pendingControl || busy) return;
		const { control, enabled } = pendingControl;
		pendingControl = null;
		secJob = 'running';
		logSource = 'security';
		logs = [];
		try {
			await api.post(`/instances/${instance.id}/security`, { control, enabled });
			await openLogStream('security');
		} catch (e) {
			secJob = 'failed';
			addToast(e instanceof Error ? e.message : 'Security update failed to start', 'error');
		}
	}

	async function openLogStream(kind: 'harden' | 'security') {
		const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
		// Single-use 60s ticket instead of the 4h JWT in the URL (audit L1).
		let credential = getToken() ?? '';
		try {
			const res = await api.get<{ ticket: string }>('/ws-ticket');
			credential = res.ticket;
		} catch {
			// Older backend without /ws-ticket — fall back to the JWT.
		}
		const path =
			kind === 'harden'
				? `/api/instances/${instance?.id}/harden/logs`
				: `/api/instances/${instance?.id}/security/logs`;
		socket = new WebSocket(`${proto}//${location.host}${path}?token=${credential}`);
		socket.onmessage = (event) => {
			const line = String(event.data);
			logs = [...logs, line];
			if (line.includes('[Dockpal Security] Error:') || line.includes('[Dockpal Hardening] Error:')) {
				if (kind === 'harden') phase = 'failed';
				else secJob = 'failed';
				socket?.close();
			} else if (
				line.includes('[Dockpal Hardening] Hardening completed successfully') ||
				line.includes('[Dockpal Security] Update completed successfully')
			) {
				if (kind === 'harden') phase = 'done';
				else secJob = 'done';
				addToast(
					kind === 'harden'
						? `"${displayName}" is hardened — password login disabled`
						: `Security change applied on "${displayName}"`,
					'success'
				);
				onchanged?.();
				socket?.close();
				detect();
			}
		};
		socket.onclose = () => {
			if (kind === 'harden' && phase === 'running') phase = 'failed';
			if (kind === 'security' && secJob === 'running') secJob = 'failed';
		};
	}
</script>

<Modal {open} title={`Server security — ${displayName}`} size="lg" onclose={close}>
	{#if instance}
		<div class="space-y-4">
			<!-- Detected security state -->
			<div class="bg-zinc-950 border border-zinc-800/60 rounded-sm p-4 space-y-2">
				<div class="flex items-center justify-between">
					<h4 class="text-xs font-semibold text-zinc-400 uppercase tracking-wider">Detected state</h4>
					<button
						class="text-xs text-zinc-500 hover:text-zinc-300 flex items-center gap-1"
						disabled={secChecking || busy}
						onclick={detect}
					>
						<Icon name="restart" class="w-3.5 h-3.5" />
						{secChecking ? 'Checking…' : 'Check again'}
					</button>
				</div>
				<div class="grid grid-cols-1 sm:grid-cols-3 gap-2 text-sm">
					<div class="flex items-center gap-2">
						<span class={`w-2 h-2 rounded-full ${secState?.password_auth === 'no' ? 'bg-green-500' : 'bg-amber-500'}`}></span>
						<span class="text-zinc-400 text-xs">Password auth:</span>
						<span class={`text-xs font-semibold ${secState?.password_auth === 'no' ? 'text-green-400' : 'text-amber-400'}`}>
							{secChecking ? '…' : pretty(secState?.password_auth ?? '')}
						</span>
					</div>
					<div class="flex items-center gap-2">
						<span class={`w-2 h-2 rounded-full ${secState?.root_login === 'no' ? 'bg-green-500' : 'bg-zinc-500'}`}></span>
						<span class="text-zinc-400 text-xs">Root login:</span>
						<span class="text-xs font-semibold text-zinc-300">{secChecking ? '…' : pretty(secState?.root_login ?? '')}</span>
					</div>
					<div class="flex items-center gap-2">
						<span class={`w-2 h-2 rounded-full ${secState?.fail2ban === 'active' ? 'bg-green-500' : 'bg-zinc-500'}`}></span>
						<span class="text-zinc-400 text-xs">fail2ban:</span>
						<span class="text-xs font-semibold text-zinc-300">{secChecking ? '…' : pretty(secState?.fail2ban ?? '')}</span>
					</div>
				</div>
				{#if secError}
					<p class="text-xs text-red-400">Detection problem: {secError}</p>
				{/if}
			</div>

			<!-- Controls -->
			<div class="space-y-2">
				<h4 class="text-xs font-semibold text-zinc-400 uppercase tracking-wider">Controls</h4>
				<div class="grid grid-cols-1 sm:grid-cols-3 gap-2">
					<Button
						variant="secondary"
						size="sm"
						disabled={busy || secChecking}
						onclick={() => askControl('password_auth', secState?.password_auth === 'no')}
					>
						{secState?.password_auth === 'no' ? 'Re-enable password login' : 'Disable password login'}
					</Button>
					<Button
						variant="secondary"
						size="sm"
						disabled={busy || secChecking}
						onclick={() => askControl('root_login', secState?.root_login === 'no')}
					>
						{secState?.root_login === 'no' ? 'Allow root login (default)' : 'Disable root login'}
					</Button>
					<Button
						variant="secondary"
						size="sm"
						disabled={busy || secChecking}
						onclick={() => askControl('fail2ban', secState?.fail2ban !== 'active')}
					>
						{secState?.fail2ban === 'active' ? 'Disable fail2ban' : 'Install & enable fail2ban'}					</Button>
				</div>
				<p class="text-xs text-zinc-600">
					Changes edit sshd via a Dockpal drop-in, validate with <span class="font-mono">sshd -t</span>, verify
					the effective config, then reload. Disabling password login is proven from the outside when the old
					password is still known.
				</p>
			</div>

			<!-- Guided hardening -->
			<div class="border-t border-zinc-800 pt-3 space-y-2">
				<div class="flex items-center gap-2">
					<h4 class="text-xs font-semibold text-zinc-400 uppercase tracking-wider">SSH key hardening</h4>
					{#if isHardened}
						<span class="inline-flex items-center gap-1.5 text-xs font-semibold px-2 py-0.5 rounded bg-green-500/10 text-green-400">
							<Icon name="admin" class="w-3.5 h-3.5" /> Hardened
							{#if instance.ssh_hardened_at}
								<span class="text-zinc-500 font-normal">— {new Date(instance.ssh_hardened_at * 1000).toLocaleString()}</span>
							{/if}
						</span>
					{:else}
						<span class="text-xs text-zinc-500">login: {authLabel}</span>
					{/if}
				</div>
				<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
					<div>
						<label for="harden-key-source" class="block text-xs font-medium text-zinc-400 mb-1">Key to install</label>
						<select
							id="harden-key-source"
							bind:value={keySource}
							disabled={busy}
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
								disabled={busy}
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
				<div>
					<span class="block text-xs font-medium text-zinc-400 mb-1">
						Your public key <span class="text-zinc-600">(recommended)</span>
					</span>
					{#if savedKeys.length > 0}
						<div class="flex flex-wrap gap-2 mb-2">
							{#each savedKeys as k (k.id)}
								<button
									type="button"
									disabled={busy}
									onclick={() => toggleExtraKey(k.id)}
									class={`text-xs px-2 py-1 rounded-sm border font-mono transition-colors ${selectedExtraKeyIDs.includes(k.id) ? 'bg-blue-600/20 border-blue-600 text-blue-300' : 'bg-zinc-950 border-zinc-800 text-zinc-400 hover:text-zinc-200'}`}
									title={k.fingerprint}
								>
									{selectedExtraKeyIDs.includes(k.id) ? '✓' : '+'} {k.name}
								</button>
							{/each}
						</div>
					{/if}
					<textarea
						id="harden-extra-keys"
						bind:value={extraPublicKeys}
						disabled={busy}
						rows="2"
						placeholder={'ssh-ed25519 AAAA... you@your-pc (one per line — from cat ~/.ssh/id_ed25519.pub)'}
						class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-xs text-white font-mono focus:outline-none focus:ring-2 focus:ring-blue-600"
					></textarea>
					<p class="text-xs text-zinc-600 mt-1">
						Pick a saved key above or paste the output of
						<span class="font-mono text-zinc-400">cat ~/.ssh/id_ed25519.pub</span> from your own
						machine — without it, only Dockpal can log in after passwords are disabled.
					</p>
				</div>
			</div>

			<!-- Run log -->
			{#if logs.length > 0}
				<div>
					<h4 class="text-xs font-semibold text-zinc-400 uppercase tracking-wider mb-1.5">
						{logSource === 'harden' ? 'Hardening log' : 'Security log'}
					</h4>
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
			{:else if secJob === 'done'}
				<div class="p-3 bg-green-500/10 border border-green-500/20 rounded-sm text-sm text-green-400">
					Security change applied — detected state refreshed above.
				</div>
			{:else if secJob === 'failed'}
				<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm text-sm text-red-400">
					Security change failed — review the log and retry.
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
	message={`Install the SSH key on "${displayName}" and disable password authentication in sshd? Key-only login is verified BEFORE passwords are turned off, and everything rolls back automatically on failure.${extraPublicKeys.trim() || selectedExtraKeyIDs.length > 0 ? ' Your own public key will also be installed so you keep CLI access.' : ' Without your own public key, only Dockpal will be able to log in.'} Keep in mind you should still have access to the provider console as a last resort.`}
	confirmLabel="Harden server"
	busy={phase === 'running'}
	onconfirm={start}
	onclose={() => (confirmOpen = false)}
/>

<ConfirmDialog
	open={pendingControl !== null}
	title="Server security change"
	message={
		pendingControl?.control === 'password_auth'
			? pendingControl?.enabled
				? `Re-enable password authentication on "${displayName}"? The Dockpal drop-in is removed and the server's own sshd config decides again.`
				: `Disable password authentication on "${displayName}"? The change is validated with sshd -t and the effective config, then proven from the outside when possible. Keep provider console access as a fallback.`
			: pendingControl?.control === 'root_login'
				? pendingControl?.enabled
					? `Stop managing root login on "${displayName}"? The server's own sshd default applies again.`
					: `Disable root login on "${displayName}" (PermitRootLogin no)? Make sure a non-root user with keys exists.`
				: pendingControl?.enabled
					? `Install and enable fail2ban on "${displayName}" with an sshd jail (maxretry=5, bantime=10m)?`
					: `Disable fail2ban on "${displayName}"? The package stays installed.`
	}
	confirmLabel="Apply"
	busy={secJob === 'running'}
	onconfirm={startControl}
	onclose={() => (pendingControl = null)}
/>
