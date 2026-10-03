<script lang="ts">
	// Security tab of the server detail page (/servers/:id): the per-server
	// security controls — SSH hardening (password auth / root login), fail2ban
	// and the ufw firewall as desired-state toggles with one Apply — plus the
	// fail2ban/firewall activity monitor. Migrated out of SSHHardeningModal
	// when the controls moved here from the Servers list.
	// Wiring (backend endpoints):
	//   GET  /api/instances/:id/security      → live detection (sshd -T, fail2ban, ufw)
	//   POST /api/instances/:id/security      → apply desired state (background job)
	//   WS   /api/instances/:id/security/logs → live job log lines
	//
	// Jobs only run when the panel holds stored SSH credentials for the
	// instance (set during Add Server). Everything is fail-closed: an unknown
	// state is displayed as NOT secured.
	import { onDestroy } from 'svelte';
	import { api, getToken } from '$lib/api/client';
	import { listSSHKeys, type SSHKeyInfo } from '$lib/api/sshkeys';
	import { addToast, isAdmin, isOperator } from '$lib/store';
	import type { InstanceListItem } from '$lib/types/api';
	import Button from '../ui/Button.svelte';
	import ConfirmDialog from '../ui/ConfirmDialog.svelte';
	import Icon from '../ui/Icon.svelte';
	import SecurityActivityCard from './SecurityActivityCard.svelte';

	interface Props {
		instanceId: string;
		/** Called after successful runs so parents can refresh cached badges. */
		onchanged?: () => void;
	}
	let { instanceId, onchanged }: Props = $props();

	type Phase = 'idle' | 'running' | 'done' | 'failed';
	interface SecState {
		password_auth: string;
		root_login: string;
		fail2ban: string;
		firewall: string;
	}

	const isLocal = $derived(instanceId === 'local');

	// Cached instance record: seeds the readout before the live SSH check and
	// carries the hardened badge / auth label afterwards.
	let instance = $state<InstanceListItem | null>(null);

	let secJob = $state<Phase>('idle');
	let logs = $state<string[]>([]);
	let savedKeys = $state<SSHKeyInfo[]>([]);
	// The operator's own public key(s) — without one, their own PC loses shell
	// access the moment password auth is disabled (only the panel key remains).
	let extraPublicKeys = $state('');
	let selectedExtraKeyIDs = $state<string[]>([]);
	let confirmApply = $state(false);
	let socket: WebSocket | null = null;
	// Live detected state.
	let secState = $state<SecState | null>(null);
	let secError = $state('');
	let secChecking = $state(false);
	// Toggle model: the desired end state, seeded from detection; Apply
	// converges the server to it. Undefined until detection has run.
	let wantPasswordAuth = $state<boolean | undefined>(undefined);
	let wantRootLogin = $state<boolean | undefined>(undefined);
	let wantFail2ban = $state<boolean | undefined>(undefined);
	let wantFirewall = $state<boolean | undefined>(undefined);

	async function loadInstance() {
		if (isLocal) {
			instance = null;
			return;
		}
		try {
			instance = await api.get<InstanceListItem>(`/instances/${encodeURIComponent(instanceId)}`);
		} catch {
			instance = null;
		}
	}

	async function detect() {
		if (isLocal || !instanceId) return;
		secChecking = true;
		secError = '';
		try {
			const res = await api.get<{ security: SecState; error?: string }>(
				`/instances/${instanceId}/security`
			);
			secState = res.security;
			secError = res.error ?? '';
			// Seed the switches once; later detections only refresh the
			// readout, not the operator's half-set toggles.
			if (wantPasswordAuth === undefined) {
				wantPasswordAuth = res.security.password_auth !== 'no';
				wantRootLogin = res.security.root_login !== 'no';
				wantFail2ban = res.security.fail2ban === 'active';
				wantFirewall = res.security.firewall === 'active';
			}
		} catch (e) {
			secError = e instanceof Error ? e.message : 'Security check failed';
		} finally {
			secChecking = false;
		}
	}

	// Reset + (re)load when the viewed server changes (deep links, switcher).
	$effect(() => {
		void instanceId;
		secJob = 'idle';
		logs = [];
		confirmApply = false;
		extraPublicKeys = '';
		selectedExtraKeyIDs = [];
		secState = null;
		secError = '';
		wantPasswordAuth = undefined;
		wantRootLogin = undefined;
		wantFail2ban = undefined;
		wantFirewall = undefined;
		loadInstance();
		if ($isOperator) detect();
		listSSHKeys()
			.then((keys) => {
				savedKeys = keys.filter((k) => k.secret_type === 'public');
			})
			.catch(() => (savedKeys = []));
	});

	onDestroy(() => {
		socket?.close();
	});

	const displayName = $derived(instance ? (isLocal ? 'This Server' : instance.name) : instanceId);
	const isHardened = $derived(instance?.ssh_hardening_status === 'hardened');
	const authLabel = $derived(
		instance?.ssh_auth_type === 'key'
			? 'SSH key'
			: instance?.ssh_auth_type === 'password'
				? 'Password'
				: 'Unknown'
	);
	const busy = $derived(secJob === 'running');

	function pretty(v: string): string {
		if (!v) return '—';
		if (v === 'unknown') return 'Unknown';
		if (v === 'prohibit-password' || v === 'without-password') return 'Keys only';
		if (v === 'inactive') return 'Inactive';
		if (v === 'active') return 'Active';
		if (v === 'absent') return 'Not installed';
		if (v === 'firewalld') return 'firewalld';
		return v === 'yes' ? 'On' : v === 'no' ? 'Off' : v;
	}

	function toggleExtraKey(id: string) {
		selectedExtraKeyIDs = selectedExtraKeyIDs.includes(id)
			? selectedExtraKeyIDs.filter((x) => x !== id)
			: [...selectedExtraKeyIDs, id];
	}

	// ---- security controls (toggle model) ----
	const togglesReady = $derived(
		wantPasswordAuth !== undefined &&
			wantRootLogin !== undefined &&
			wantFail2ban !== undefined &&
			wantFirewall !== undefined
	);
	const togglesDirty = $derived(
		togglesReady &&
			secState !== null &&
			(wantPasswordAuth !== (secState.password_auth !== 'no') ||
				wantRootLogin !== (secState.root_login !== 'no') ||
				wantFail2ban !== (secState.fail2ban === 'active') ||
				wantFirewall !== (secState.firewall === 'active'))
	);
	// firewalld servers are detected but not managed by Dockpal.
	const firewallUnmanaged = $derived(secState?.firewall === 'firewalld');

	async function applyState() {
		if (!instanceId || !togglesReady || busy) return;
		secJob = 'running';
		logs = [];
		try {
			const savedLines = selectedExtraKeyIDs
				.map((id) => savedKeys.find((k) => k.id === id)?.public_key?.trim())
				.filter((l): l is string => !!l);
			const pastedLines = extraPublicKeys
				.split('\n')
				.map((l) => l.trim())
				.filter(Boolean);
			await api.post(`/instances/${instanceId}/security`, {
				password_auth: wantPasswordAuth,
				root_login: wantRootLogin,
				fail2ban: wantFail2ban,
				firewall: wantFirewall,
				extra_public_keys: [...savedLines, ...pastedLines]
			});
			await openLogStream();
		} catch (e) {
			secJob = 'failed';
			addToast(e instanceof Error ? e.message : 'Security update failed to start', 'error');
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
			`${proto}//${location.host}/api/instances/${instanceId}/security/logs?token=${credential}`
		);
		socket.onmessage = (event) => {
			const line = String(event.data);
			logs = [...logs, line];
			if (line.includes('[Dockpal Security] Error:')) {
				secJob = 'failed';
				socket?.close();
			} else if (line.includes('[Dockpal Security] Update completed successfully')) {
				secJob = 'done';
				addToast(`Security changes applied on "${displayName}"`, 'success');
				onchanged?.();
				socket?.close();
				loadInstance();
				detect();
			}
		};
		socket.onclose = () => {
			if (secJob === 'running') secJob = 'failed';
		};
	}
</script>

<div class="space-y-6">
	{#if isLocal}
		<!-- The local instance is not managed over SSH: nothing to harden. -->
		<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
			<p class="text-sm text-zinc-400">
				This is the panel's own host — SSH hardening, fail2ban and firewall controls apply to
				remote servers managed over SSH only.
			</p>
		</div>
	{:else}
		<!-- Detected state + controls -->
		<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4 space-y-4">
			<div class="flex items-center justify-between gap-2 flex-wrap">
				<h3 class="text-sm font-medium text-white">SSH access &amp; hardening</h3>
				<button
					class="text-xs text-zinc-500 hover:text-zinc-300 flex items-center gap-1"
					disabled={secChecking || busy || !$isOperator}
					onclick={detect}
				>
					<Icon name="restart" class="w-3.5 h-3.5" />
					{secChecking ? 'Checking…' : 'Check again'}
				</button>
			</div>

			<!-- Detected security state -->
			<div class="bg-zinc-950 border border-zinc-800/60 rounded-sm p-3 space-y-2">
				<div class="grid grid-cols-1 sm:grid-cols-4 gap-2 text-sm">
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
					<div class="flex items-center gap-2">
						<span class={`w-2 h-2 rounded-full ${secState?.firewall === 'active' ? 'bg-green-500' : 'bg-zinc-500'}`}></span>
						<span class="text-zinc-400 text-xs">Firewall:</span>
						<span class="text-xs font-semibold text-zinc-300">{secChecking ? '…' : pretty(secState?.firewall ?? '')}</span>
					</div>
				</div>
				{#if secError}
					<p class="text-xs text-red-400">Detection problem: {secError}</p>
				{/if}
				{#if !$isOperator}
					<p class="text-xs text-zinc-500">Live detection requires Operator access.</p>
				{/if}
			</div>

			<!-- Controls (toggle model: set the switches, then Apply) -->
			<div class="space-y-2">
				<div class="flex items-center justify-between">
					<h4 class="text-xs font-semibold text-zinc-400 uppercase tracking-wider">Controls</h4>
					{#if togglesDirty}
						<span class="text-[11px] text-amber-400">unsaved changes</span>
					{/if}
				</div>
				<div class="divide-y divide-zinc-800/60 rounded-sm border border-zinc-800/60 bg-zinc-950/40">
					<label class="flex items-center justify-between gap-3 px-4 py-2.5 cursor-pointer">
						<span>
							<span class="block text-sm text-zinc-200">Password login</span>
							<span class="block text-xs text-zinc-600">allow SSH passwords for this server</span>
						</span>
						<input
							type="checkbox"
							checked={wantPasswordAuth}
							disabled={busy || secChecking || wantPasswordAuth === undefined || !$isAdmin}
							onchange={(e) => (wantPasswordAuth = e.currentTarget.checked)}
							class="h-5 w-9 shrink-0 cursor-pointer appearance-none rounded-full bg-zinc-700 checked:bg-green-600 transition-colors relative before:absolute before:top-0.5 before:left-0.5 before:h-4 before:w-4 before:rounded-full before:bg-white before:transition-transform checked:before:translate-x-4"
						/>
					</label>
					<label class="flex items-center justify-between gap-3 px-4 py-2.5 cursor-pointer">
						<span>
							<span class="block text-sm text-zinc-200">Root login</span>
							<span class="block text-xs text-zinc-600">PermitRootLogin no when off — needs a non-root key user first</span>
						</span>
						<input
							type="checkbox"
							checked={wantRootLogin}
							disabled={busy || secChecking || wantRootLogin === undefined || !$isAdmin}
							onchange={(e) => (wantRootLogin = e.currentTarget.checked)}
							class="h-5 w-9 shrink-0 cursor-pointer appearance-none rounded-full bg-zinc-700 checked:bg-green-600 transition-colors relative before:absolute before:top-0.5 before:left-0.5 before:h-4 before:w-4 before:rounded-full before:bg-white before:transition-transform checked:before:translate-x-4"
						/>
					</label>
					<label class="flex items-center justify-between gap-3 px-4 py-2.5 cursor-pointer">
						<span>
							<span class="block text-sm text-zinc-200">fail2ban</span>
							<span class="block text-xs text-zinc-600">brute-force protection (sshd jail, maxretry=5, bantime=10m)</span>
						</span>
						<input
							type="checkbox"
							checked={wantFail2ban}
							disabled={busy || secChecking || wantFail2ban === undefined || !$isAdmin}
							onchange={(e) => (wantFail2ban = e.currentTarget.checked)}
							class="h-5 w-9 shrink-0 cursor-pointer appearance-none rounded-full bg-zinc-700 checked:bg-green-600 transition-colors relative before:absolute before:top-0.5 before:left-0.5 before:h-4 before:w-4 before:rounded-full before:bg-white before:transition-transform checked:before:translate-x-4"
						/>
					</label>
					<label class="flex items-center justify-between gap-3 px-4 py-2.5 cursor-pointer">
						<span>
							<span class="block text-sm text-zinc-200">Firewall (ufw)</span>
							<span class="block text-xs text-zinc-600">
								default-deny inbound — the SSH port is allowed before enable;
								Docker publishes container ports outside ufw
								{#if firewallUnmanaged}
									— firewalld detected, not managed here
								{/if}
							</span>
						</span>
						<input
							type="checkbox"
							checked={wantFirewall}
							disabled={busy || secChecking || wantFirewall === undefined || !$isAdmin || firewallUnmanaged}
							onchange={(e) => (wantFirewall = e.currentTarget.checked)}
							class="h-5 w-9 shrink-0 cursor-pointer appearance-none rounded-full bg-zinc-700 checked:bg-green-600 transition-colors relative before:absolute before:top-0.5 before:left-0.5 before:h-4 before:w-4 before:rounded-full before:bg-white before:transition-transform checked:before:translate-x-4"
						/>
					</label>
				</div>
				{#if $isAdmin}
					<div class="border-t border-zinc-800/60 pt-3 mt-1">
						<span class="block text-xs font-medium text-zinc-400 mb-1">
							Your public keys on this server <span class="text-zinc-600">(installed with Apply)</span>
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
							These plus the panel key are (re)installed idempotently on every Apply — even with
							password login left ON, so switching to key-only later needs no re-bootstrap. Without
							your own key here, only Dockpal can log in once passwords are off.
						</p>
					</div>
					<p class="text-xs text-zinc-600">
						Apply converges the server to these switches: sshd changes go through one drop-in write,
						<span class="font-mono">sshd -t</span> + effective-config check + reload; ufw installs on
						demand and allows the SSH port before enabling; only differences vs the detected state
						are touched.
					</p>
				{:else}
					<p class="text-xs text-zinc-500">Admin access is required to apply changes.</p>
				{/if}
			</div>

			<!-- Hardened badge (result of turning password login off) -->
			{#if isHardened}
				<div class="flex items-center gap-2">
					<span class="inline-flex items-center gap-1.5 text-xs font-semibold px-2 py-0.5 rounded bg-green-500/10 text-green-400">
						<Icon name="admin" class="w-3.5 h-3.5" /> Hardened
						{#if instance?.ssh_hardened_at}
							<span class="text-zinc-500 font-normal">— {new Date(instance.ssh_hardened_at * 1000).toLocaleString()}</span>
						{/if}
					</span>
					<span class="text-xs text-zinc-500">login: {authLabel}</span>
				</div>
			{/if}

			<!-- Run log -->
			{#if logs.length > 0}
				<div>
					<h4 class="text-xs font-semibold text-zinc-400 uppercase tracking-wider mb-1.5">Security log</h4>
					<pre class="p-3 bg-black border border-zinc-800 rounded-sm text-xs text-zinc-300 font-mono whitespace-pre-wrap overflow-auto max-h-56">{logs.join('\n')}</pre>
				</div>
			{/if}

			{#if secJob === 'done'}
				<div class="p-3 bg-green-500/10 border border-green-500/20 rounded-sm text-sm text-green-400">
					Changes applied — the panel key and your public keys are installed, the toggles are
					enforced, and the detected state above is refreshed from the server.
				</div>
			{:else if secJob === 'failed'}
				<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm text-sm text-red-400">
					Apply failed — the keys may have been installed (safe), but the toggles were not fully
					enforced. Review the log and retry.
				</div>
			{/if}

			{#if $isAdmin}
				<div class="flex justify-end">
					{#if secJob === 'running'}
						<Button variant="primary" size="sm" loading={true}>Applying…</Button>
					{:else}
						<Button variant="primary" size="sm" disabled={!togglesReady || busy} onclick={() => (confirmApply = true)}>
							Apply
						</Button>
					{/if}
				</div>
			{/if}
		</div>
	{/if}

	<!-- fail2ban + firewall activity monitor (collapsed by default, SSH only
	     fires when the user expands or refreshes — server-side TTL cache). -->
	<SecurityActivityCard {instanceId} fail2banState={instance?.sec_fail2ban ?? ''} />
</div>

{#if confirmApply}
	<ConfirmDialog
		open={confirmApply}
		title="Apply security changes"
		message={`Converge "${displayName}" to the selected state? Only the differences vs the detected state are applied — sshd edits are validated and reloaded, ufw allows the SSH port before enabling, and disabling password login is verified from the outside when possible. Keep provider console access as a fallback.`}
		confirmLabel="Apply changes"
		busy={secJob === 'running'}
		onconfirm={applyState}
		onclose={() => (confirmApply = false)}
	/>
{/if}
