<script lang="ts">
	// Add Server panel — registers a remote VPS as a managed instance and
	// optionally installs the dockpal-agent over SSH with live log streaming.
	// Wiring (all pre-existing backend endpoints):
	//   POST /api/instances                       → create record + token
	//   POST /api/instances/:id/install           → SSH installer (docker + agent)
	//   WS   /api/instances/:id/install/logs      → live install log lines
	//   POST /api/instances/:id/test              → connectivity check
	import { onDestroy } from 'svelte';
	import { api } from '$lib/api/client';
	import { instanceWSURL, wsCredential } from '$lib/ws';
	import { listSSHKeys, type SSHKeyInfo } from '$lib/api/sshkeys';
	import { addToast } from '$lib/store';
	import Button from '../ui/Button.svelte';

	type Step = 'form' | 'created' | 'installing' | 'done';
	// Process tabs: 1 register → 2 install agent → 3 install log. A tab is
	// clickable only once its predecessor produced something (instanceId for
	// tab 2, a started install for tab 3).
	const tabs = [
		{ n: 1, label: 'Register server' },
		{ n: 2, label: 'Install agent' },
		{ n: 3, label: 'Install log' }
	] as const;

	let step = $state<Step>('form');

	// --- tab 1: register ---
	let name = $state('');
	let host = $state('');
	let port = $state<number | null>(9273);
	let mode = $state<'edge' | 'direct'>('edge');
	// SSH user is asked up front on tab 1 (with host/user in direct mode) so the
	// tab-2 panel-key command can be personalized instead of "user@your-server".
	let sshUser = $state('root');
	let creating = $state(false);
	let createError = $state('');

	// --- step 2/3: install + logs ---
	let instanceId = $state('');
	let installCommand = $state('');
	// Panel key bootstrap: the panel generates a keypair at create time; the
	// operator authorizes the public half on the server (ssh-copy-id style),
	// then the install connects with the panel key — no password, no private
	// key from this PC ever uploaded.
	let panelPublicKey = $state('');
	let panelKeyFingerprint = $state('');
	let panelKeySetupCommand = $state('');
	let sshHost = $state('');
	let sshPort = $state<number | null>(22);
	let sshAuthType = $state<'panel_key' | 'password' | 'key'>('panel_key');
	let sshSecret = $state('');
	// Key auth source: a saved LEGACY private key or an ad-hoc paste. (Saved
	// keys are now public-only for hardening; private ones are legacy.)
	let keySource = $state<'saved' | 'paste'>('paste');
	let savedKeys = $state<SSHKeyInfo[]>([]);
	let selectedKeyID = $state('');
	let installDocker = $state(true);
	// Address the agent should use to reach this panel (edge mode). Empty =
	// use the browser's current host, which is wrong when the panel is
	// opened via localhost — the VPS would dial its own localhost.
	let panelAddress = $state('');
	let installing = $state(false);
	let logs = $state<string[]>([]);
	// True when the install session ended with the installer's error marker;
	// drives the "Retry install" affordance on the done step.
	let installFailed = $state(false);
	let showManualCommand = $state(false);
	let showPanelKeyDetails = $state(true);
	let testing = $state(false);
	let testMessage = $state('');
	let testOk = $state(false);

	let socket: WebSocket | null = null;

	// Saved keys feed the legacy "Saved private key" picker only — public keys
	// cannot authenticate the installer (only hardening uses those).
	$effect(() => {
		listSSHKeys()
			.then((keys) => {
				savedKeys = keys.filter((k) => k.secret_type !== 'public');
				if (savedKeys.length > 0 && !selectedKeyID) {
					keySource = 'saved';
					selectedKeyID = savedKeys[0].id;
				}
			})
			.catch(() => {
				// Not an admin or endpoint unavailable — paste remains the path.
			});
	});

	onDestroy(() => {
		socket?.close();
	});

	const canCreate = $derived(
		name.trim() !== '' &&
			(mode !== 'direct' || host.trim() !== '') &&
			sshUser.trim() !== '' &&
			!creating
	);
	// Connection fields live on tab 1 for direct mode (host/user entered there);
	// for edge they can be adjusted on tab 2 (defaults to the tab-1 user).
	const effectiveSSHHost = $derived(mode === 'direct' ? host.trim() : sshHost.trim());
	const effectiveSSHUser = $derived(sshUser.trim() || 'root');
	const effectiveSSHPort = $derived(sshPort ?? 22);
	// Setup command personalized with the actual user/host once known — no more
	// "ssh user@your-server …" guesswork for the operator.
	const personalizedSetupCommand = $derived(
		panelKeySetupCommand.replace(
			'user@your-server',
			`${effectiveSSHUser}@${effectiveSSHHost || 'your-server'}`
		)
	);
	// Panel-key auth needs no secret at all; key auth with a saved key only
	// needs the key picked; paste mode needs the secret textarea filled.
	const keyCredentialOk = $derived(
		sshAuthType === 'panel_key' ||
			sshAuthType === 'password' ||
			(keySource === 'saved' ? selectedKeyID !== '' : sshSecret.trim() !== '')
	);
	const canInstall = $derived(
		effectiveSSHHost !== '' && keyCredentialOk && !installing
	);
	const canTest = $derived(!testing);

	function reset() {
		socket?.close();
		socket = null;
		step = 'form';
		name = '';
		host = '';
		port = 9273;
		mode = 'edge';
		sshUser = 'root';
		sshHost = '';
		sshPort = 22;
		sshAuthType = 'panel_key';
		keySource = 'paste';
		selectedKeyID = savedKeys[0]?.id ?? '';
		createError = '';
		instanceId = '';
		installCommand = '';
		panelPublicKey = '';
		panelKeyFingerprint = '';
		panelKeySetupCommand = '';
		sshSecret = '';
		logs = [];
		installFailed = false;
		testMessage = '';
		testOk = false;
		showManualCommand = false;
		showPanelKeyDetails = true;
	}

	// Go back to the install form (fields kept, including the SSH secret) so a
	// failed install can be corrected and re-run without re-registering.
	function retryInstall() {
		socket?.close();
		socket = null;
		installFailed = false;
		logs = [];
		testMessage = '';
		testOk = false;
		step = 'created';
	}

	async function create() {
		if (!canCreate) return;
		creating = true;
		createError = '';
		try {
			const res = await api.post<{
				id: string;
				install_command?: string;
				ssh_public_key?: string;
				ssh_key_fingerprint?: string;
				ssh_key_setup_command?: string;
			}>('/instances', {
				name: name.trim(),
				host: mode === 'direct' ? host.trim() : '',
				port: mode === 'direct' ? (port ?? 0) : 0,
				mode
			});
			instanceId = res.id;
			installCommand = res.install_command ?? '';
			panelPublicKey = res.ssh_public_key ?? '';
			panelKeyFingerprint = res.ssh_key_fingerprint ?? '';
			panelKeySetupCommand = res.ssh_key_setup_command ?? '';
			step = 'created';
			addToast(`Instance "${name.trim()}" created`, 'success');
		} catch (e) {
			createError = e instanceof Error ? e.message : 'Failed to create instance';
		} finally {
			creating = false;
		}
	}

	async function openLogStream() {
		// Single-use 60s ticket instead of the 4h JWT in the URL (audit L1).
		const credential = await wsCredential();
		socket = new WebSocket(instanceWSURL(instanceId, '/install/logs', credential));
		socket.onmessage = (event) => {
			const line = String(event.data);
			logs = [...logs, line];
			// The installer's last line is deterministic (error or success); the
			// session manager never closes listener channels itself, so detect
			// the final message here and end the stream.
			if (line.includes('[Dockpal Installer] Error:')) {
				installFailed = true;
				installing = false;
				step = 'done';
				socket?.close();
			} else if (line.includes('Installation completed successfully')) {
				installing = false;
				step = 'done';
				socket?.close();
			}
		};
		socket.onclose = () => {
			if (installing) {
				// Server dropped the stream without a final marker (restart, etc.)
				installing = false;
				step = 'done';
			}
		};
	}

	async function startInstall() {
		if (!canInstall) return;
		installing = true;
		logs = [];
		installFailed = false;
		step = 'installing';
		try {
			await api.post(`/instances/${instanceId}/install`, {
				ssh_host: effectiveSSHHost,
				ssh_port: effectiveSSHPort,
				ssh_user: effectiveSSHUser,
				ssh_auth_type: sshAuthType,
				// Panel-key auth needs no secret — the panel uses its own
				// generated keypair. Saved keys are referenced by ID (legacy
				// private keys only); pasted keys travel inline as before.
				ssh_secret:
					sshAuthType === 'key' && keySource === 'paste' ? sshSecret : undefined,
				ssh_key_id: sshAuthType === 'key' && keySource === 'saved' ? selectedKeyID : undefined,
				install_docker: installDocker,
				panel_address: panelAddress.trim() || undefined
			});
			openLogStream();
		} catch (e) {
			installing = false;
			step = 'created';
			addToast(e instanceof Error ? e.message : 'Install failed to start', 'error');
		}
	}

	async function testConnection() {
		if (!canTest || !instanceId) return;
		testing = true;
		testMessage = '';
		try {
			const res = await api.post<{ status: string; message: string }>(
				`/instances/${instanceId}/test`
			);
			testOk = res.status === 'ok';
			testMessage = res.message;
		} catch (e) {
			testOk = false;
			testMessage = e instanceof Error ? e.message : 'Test failed';
		} finally {
			testing = false;
		}
	}
</script>

<div class="max-w-2xl space-y-4">
	<!-- Process tab bar -->
	<div class="flex gap-1 border-b border-zinc-800">
		{#each tabs as tab (tab.n)}
			{@const active =
				(tab.n === 1 && step === 'form') ||
				(tab.n === 2 && step === 'created') ||
				(tab.n === 3 && (step === 'installing' || step === 'done'))}
			{@const clickable =
				(tab.n === 1 && step !== 'form') ||
				(tab.n === 2 && instanceId !== '' && step !== 'created' && step !== 'installing')}
			<button
				onclick={() => {
					if (tab.n === 1 && step !== 'form') step = 'form';
					else if (tab.n === 2 && instanceId && step !== 'installing') step = 'created';
				}}
				disabled={!clickable && !active}
				class="px-3 py-2 text-sm transition-colors border-b-2 -mb-px disabled:cursor-not-allowed"
				class:border-white={active}
				class:text-white={active}
				class:border-transparent={!active}
				class:text-zinc-500={!active}
				class:hover:text-zinc-300={!active && clickable}
			>
				{tab.n} · {tab.label}
				{#if tab.n === 1 && instanceId && step !== 'form'}
					<span class="text-emerald-400">✓</span>
				{/if}
				{#if tab.n === 3 && step === 'installing'}
					<span class="text-blue-400 animate-pulse">…</span>
				{/if}
			</button>
		{/each}
	</div>

	<!-- Tab 1 — register server -->
	{#if step === 'form'}
		<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-5">
			<div class="flex items-center gap-2 mb-4">
				<h3 class="text-sm font-semibold text-white">Register server</h3>
				{#if instanceId}
					<span class="ml-auto text-xs text-emerald-400">✓ created: {instanceId}</span>
				{/if}
			</div>
			<form class="space-y-3" onsubmit={(e) => { e.preventDefault(); create(); }}>
				<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
					<div>
						<label for="inst-name" class="block text-xs font-medium text-zinc-400 mb-1">Server name</label>
						<input
							id="inst-name"
							type="text"
							bind:value={name}
							placeholder="vps-singapore"
							class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
						/>
					</div>
					<div>
						<label for="inst-mode" class="block text-xs font-medium text-zinc-400 mb-1">Connection mode</label>
						<select
							id="inst-mode"
							bind:value={mode}
							class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
						>
							<option value="edge">Edge — agent dials this panel (NAT friendly)</option>
							<option value="direct">Direct — panel dials agent host</option>
						</select>
					</div>
				</div>

				{#if mode === 'direct'}
					<div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
						<div>
							<label for="inst-host" class="block text-xs font-medium text-zinc-400 mb-1">Host</label>
							<input
								id="inst-host"
								type="text"
								bind:value={host}
								placeholder="203.0.113.10"
								class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
							/>
						</div>
						<div>
							<label for="inst-ssh-user" class="block text-xs font-medium text-zinc-400 mb-1">SSH user</label>
							<input
								id="inst-ssh-user"
								type="text"
								bind:value={sshUser}
								placeholder="root"
								class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
							/>
						</div>
						<div>
							<label for="inst-port" class="block text-xs font-medium text-zinc-400 mb-1">Agent port</label>
							<input
								id="inst-port"
								type="number"
								min="1"
								max="65535"
								bind:value={port}
								class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
							/>
						</div>
					</div>
				{:else}
					<div class="sm:w-1/2">
						<label for="inst-ssh-user" class="block text-xs font-medium text-zinc-400 mb-1">SSH user</label>
						<input
							id="inst-ssh-user"
							type="text"
							bind:value={sshUser}
							placeholder="root"
							class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
						/>
					</div>
				{/if}

				{#if createError}
					<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm text-sm text-red-400">{createError}</div>
				{/if}

				<div class="flex justify-end">
					<Button type="submit" variant="primary" loading={creating} disabled={!canCreate}>Create instance</Button>
				</div>
			</form>
		</div>
	{/if}

	<!-- Tab 2 — install agent -->
	{#if step === 'created'}
		<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-5">
			<div class="flex items-center gap-2 mb-4">
				<h3 class="text-sm font-semibold text-white">Install agent</h3>
				<span class="ml-auto text-xs text-zinc-500">{name}</span>
			</div>

			<!-- SSH auto-install -->
			<div class="space-y-3">
				<!-- Panel key bootstrap: authorize the panel's public key on the
				     server, then install without any password or private key. -->
				{#if panelPublicKey}
					<div class="bg-zinc-950 border border-zinc-800/60 rounded-sm p-4 space-y-2">
						<div class="flex items-center justify-between">
							<h4 class="text-xs font-semibold text-zinc-400 uppercase tracking-wider">
								Panel public key <span class="text-emerald-400 normal-case">(recommended login method)</span>
							</h4>
							<button
								class="text-xs text-zinc-500 hover:text-zinc-300"
								onclick={() => (showPanelKeyDetails = !showPanelKeyDetails)}
							>
								{showPanelKeyDetails ? '▾ hide' : '▸ show'}
							</button>
						</div>
						{#if showPanelKeyDetails}
							<p class="text-xs text-zinc-500">
								Authorize this key on the new server (pick any one method), then choose
								<span class="text-zinc-300">"Panel key"</span> below — no password needed and
								your own private key never leaves your PC.
							</p>
							<div>
								<div class="flex items-center justify-between mb-1">
									<span class="text-[11px] text-zinc-500">1 · run from any machine that can reach the server (or via provider console / cloud-init)</span>
									<button
										class="text-xs text-blue-400 hover:text-blue-300"
										onclick={() => {
											navigator.clipboard?.writeText(personalizedSetupCommand);
											addToast('Setup command copied', 'success');
										}}
									>
										Copy command
									</button>
								</div>
								<pre class="p-2 bg-black border border-zinc-800 rounded-sm text-[11px] text-zinc-300 font-mono whitespace-pre-wrap break-all overflow-auto max-h-24">{personalizedSetupCommand}</pre>
							</div>
							<div>
								<div class="flex items-center justify-between mb-1">
									<span class="text-[11px] text-zinc-500">2 · or paste into the provider's "SSH keys" field at VPS creation</span>
									<button
										class="text-xs text-blue-400 hover:text-blue-300"
										onclick={() => {
											navigator.clipboard?.writeText(panelPublicKey);
											addToast('Public key copied', 'success');
										}}
									>
										Copy public key
									</button>
								</div>
								<pre class="p-2 bg-black border border-zinc-800 rounded-sm text-[11px] text-zinc-400 font-mono whitespace-pre-wrap break-all overflow-auto max-h-16">{panelPublicKey}</pre>
							</div>
							{#if panelKeyFingerprint}
								<p class="text-[11px] text-zinc-600 font-mono">fingerprint: {panelKeyFingerprint}</p>
							{/if}
						{/if}
					</div>
				{/if}

				<!-- Connection info — from tab 1 (direct) or editable here (edge). -->
				{#if mode === 'direct'}
					<div class="flex flex-wrap items-center gap-x-4 gap-y-1 px-3 py-2 bg-zinc-950 border border-zinc-800/60 rounded-sm text-xs">
						<span class="text-zinc-500">Connecting to</span>
						<span class="font-mono text-zinc-200">{effectiveSSHUser}@{effectiveSSHHost}</span>
						<span class="text-zinc-600">SSH port {effectiveSSHPort}</span>
						<button class="text-blue-400 hover:text-blue-300" onclick={() => (step = 'form')}>edit on tab 1</button>
					</div>
				{:else}
					<div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
						<div>
							<label for="ssh-host" class="block text-xs font-medium text-zinc-400 mb-1">SSH host</label>
							<input
								id="ssh-host"
								type="text"
								bind:value={sshHost}
								placeholder="VPS IP or hostname"
								class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
							/>
						</div>
						<div>
							<label for="ssh-port" class="block text-xs font-medium text-zinc-400 mb-1">SSH port</label>
							<input
								id="ssh-port"
								type="number"
								min="1"
								max="65535"
								bind:value={sshPort}
								class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
							/>
						</div>
						<div>
							<label for="ssh-user" class="block text-xs font-medium text-zinc-400 mb-1">SSH user</label>
							<input
								id="ssh-user"
								type="text"
								bind:value={sshUser}
								placeholder="root"
								class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
							/>
						</div>
					</div>
				{/if}
				<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
					<div>
						<label for="ssh-auth" class="block text-xs font-medium text-zinc-400 mb-1">Auth type</label>
						<select
							id="ssh-auth"
							bind:value={sshAuthType}
							class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
						>
							<option value="panel_key">Panel key (recommended)</option>
							<option value="password">Password</option>
							<option value="key">Private key</option>
						</select>
					</div>
					{#if sshAuthType === 'password'}
						<div>
							<label for="ssh-secret" class="block text-xs font-medium text-zinc-400 mb-1">Password</label>
							<input
								id="ssh-secret"
								type="password"
								bind:value={sshSecret}
								class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
							/>
						</div>
					{:else if sshAuthType === 'key'}
						<div>
							<label for="ssh-key-source" class="block text-xs font-medium text-zinc-400 mb-1">Key source</label>
							<select
								id="ssh-key-source"
								bind:value={keySource}
								class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
							>
								<option value="saved">Saved private key (legacy)</option>
								<option value="paste">Paste key</option>
							</select>
						</div>
					{:else}
						<div class="flex items-end">
							<p class="text-xs text-zinc-600 pb-2">
								Uses the panel key above — nothing to type.
							</p>
						</div>
					{/if}
				</div>
				{#if sshAuthType === 'key' && keySource === 'saved'}
					<div>
						<label for="ssh-saved-key" class="block text-xs font-medium text-zinc-400 mb-1">Saved private key (legacy)</label>
						<select
							id="ssh-saved-key"
							bind:value={selectedKeyID}
							class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
						>
							{#each savedKeys as k (k.id)}
								<option value={k.id}>{k.name} — {k.key_type} {k.fingerprint.slice(0, 20)}…</option>
							{:else}
								<option value="">No legacy private keys — use "Panel key" or "Password" instead</option>
							{/each}
						</select>
					</div>
				{:else if sshAuthType === 'key' && keySource === 'paste'}
					<div>
						<label for="ssh-secret" class="block text-xs font-medium text-zinc-400 mb-1">Private key</label>
						<textarea
							id="ssh-secret"
							bind:value={sshSecret}
							rows="3"
							placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
							class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white font-mono focus:outline-none focus:ring-2 focus:ring-blue-600"
						></textarea>
					</div>
				{/if}
				<label class="flex items-center gap-2 text-xs text-zinc-400">
					<input type="checkbox" bind:checked={installDocker} class="accent-blue-600" />
					Install Docker if missing (via get.docker.com)
				</label>
				{#if mode === 'edge'}
					<div>
						<label for="panel-address" class="block text-xs font-medium text-zinc-400 mb-1">
							Panel address as agents see it <span class="text-zinc-600">(optional)</span>
						</label>
						<input
							id="panel-address"
							type="text"
							bind:value={panelAddress}
							placeholder={location.host}
							class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
						/>
						<p class="text-xs text-zinc-600 mt-1">
							Leave empty to use "{location.host}". Must be reachable from the remote server —
							"localhost" only works when the agent runs on this same machine.
						</p>
					</div>
				{/if}
				<div class="flex justify-end gap-2">
					<Button
						variant="primary"
						loading={installing}
						disabled={!canInstall}
						onclick={startInstall}
					>
						Install over SSH
					</Button>
				</div>
				<p class="text-xs text-zinc-600">
					SSH credentials are encrypted at rest and used once by the installer. With "Panel key"
					auth nothing sensitive is typed at all — the panel connects with its own key.
				</p>
			</div>

			<!-- Manual fallback -->
			<div class="mt-4 border-t border-zinc-800 pt-3">
				<button
					class="text-xs text-zinc-500 hover:text-zinc-300"
					onclick={() => (showManualCommand = !showManualCommand)}
				>
					{showManualCommand ? '▾' : '▸'} Or install manually on the server
				</button>
				{#if showManualCommand}
					<pre class="mt-2 p-3 bg-black border border-zinc-800 rounded-sm text-xs text-zinc-300 font-mono whitespace-pre-wrap overflow-auto max-h-48">{installCommand}</pre>
					<button
						class="mt-1 text-xs text-blue-400 hover:text-blue-300"
						onclick={() => {
							navigator.clipboard?.writeText(installCommand);
							addToast('Install command copied', 'success');
						}}
					>
						Copy command
					</button>
				{/if}
			</div>
		</div>
	{/if}

	<!-- Tab 3 — live logs + verify -->
	{#if step === 'installing' || step === 'done'}
		<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-5">
			<div class="flex items-center gap-2 mb-3">
				<h3 class="text-sm font-semibold text-white">Install log</h3>
				{#if step === 'installing'}
					<span class="ml-auto text-xs text-blue-400 animate-pulse">installing…</span>
				{:else}
					<span class="ml-auto text-xs text-emerald-400">session finished</span>
				{/if}
			</div>
			<pre class="p-3 bg-black border border-zinc-800 rounded-sm text-xs text-zinc-300 font-mono whitespace-pre-wrap overflow-auto max-h-64">{logs.join('\n')}</pre>

			{#if step === 'done'}
				<div class="mt-3 flex items-center justify-between gap-3">
					<div class="text-xs">
						{#if installFailed}
							<span class="text-red-400">Install failed — fix the issue and retry.</span>
						{:else if testMessage}
							<span class={testOk ? 'text-emerald-400' : 'text-red-400'}>{testMessage}</span>
						{:else}
							<span class="text-zinc-500">Verify the agent connection:</span>
						{/if}
					</div>
					<div class="flex items-center gap-2">
						{#if installFailed}
							<Button variant="secondary" size="sm" onclick={retryInstall}>Retry install</Button>
						{/if}
						<Button variant="secondary" size="sm" loading={testing} disabled={!canTest} onclick={testConnection}>
							Test connection
						</Button>
					</div>
				</div>
			{/if}
		</div>

		{#if step === 'done'}
			<div class="flex justify-end">
				<Button variant="secondary" size="sm" onclick={reset}>Add another server</Button>
			</div>
		{/if}
	{/if}
</div>
