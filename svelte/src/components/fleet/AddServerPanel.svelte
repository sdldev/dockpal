<script lang="ts">
	// Add Server panel — registers a remote VPS as a managed instance and
	// optionally installs the dockpal-agent over SSH with live log streaming.
	// Wiring (all pre-existing backend endpoints):
	//   POST /api/instances                       → create record + token
	//   POST /api/instances/:id/install           → SSH installer (docker + agent)
	//   WS   /api/instances/:id/install/logs      → live install log lines
	//   POST /api/instances/:id/test              → connectivity check
	import { onDestroy } from 'svelte';
	import { api, getToken } from '$lib/api/client';
	import { addToast } from '$lib/store';
	import Button from '../ui/Button.svelte';

	type Step = 'form' | 'created' | 'installing' | 'done';

	let step = $state<Step>('form');

	// --- step 1: create ---
	let name = $state('');
	let host = $state('');
	let port = $state<number | null>(9273);
	let mode = $state<'edge' | 'direct'>('edge');
	let creating = $state(false);
	let createError = $state('');

	// --- step 2/3: install + logs ---
	let instanceId = $state('');
	let installCommand = $state('');
	let sshHost = $state('');
	let sshPort = $state<number | null>(22);
	let sshUser = $state('root');
	let sshAuthType = $state<'password' | 'key'>('password');
	let sshSecret = $state('');
	let installDocker = $state(true);
	// Address the agent should use to reach this panel (edge mode). Empty =
	// use the browser's current host, which is wrong when the panel is
	// opened via localhost — the VPS would dial its own localhost.
	let panelAddress = $state('');
	let installing = $state(false);
	let logs = $state<string[]>([]);
	let showManualCommand = $state(false);
	let testing = $state(false);
	let testMessage = $state('');
	let testOk = $state(false);

	let socket: WebSocket | null = null;

	onDestroy(() => {
		socket?.close();
	});

	const canCreate = $derived(name.trim() !== '' && !creating);
	// Host is only meaningful for direct mode (edge agents dial the panel).
	const canInstall = $derived(
		sshHost.trim() !== '' && sshSecret.trim() !== '' && !installing
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
		createError = '';
		instanceId = '';
		installCommand = '';
		sshSecret = '';
		logs = [];
		testMessage = '';
		testOk = false;
		showManualCommand = false;
	}

	async function create() {
		if (!canCreate) return;
		creating = true;
		createError = '';
		try {
			const res = await api.post<{ id: string; install_command?: string }>('/instances', {
				name: name.trim(),
				host: mode === 'direct' ? host.trim() : '',
				port: mode === 'direct' ? (port ?? 0) : 0,
				mode
			});
			instanceId = res.id;
			installCommand = res.install_command ?? '';
			step = 'created';
			addToast(`Instance "${name.trim()}" created`, 'success');
		} catch (e) {
			createError = e instanceof Error ? e.message : 'Failed to create instance';
		} finally {
			creating = false;
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
			`${proto}//${location.host}/api/instances/${instanceId}/install/logs?token=${credential}`
		);
		socket.onmessage = (event) => {
			const line = String(event.data);
			logs = [...logs, line];
			// The installer's last line is deterministic (error or success); the
			// session manager never closes listener channels itself, so detect
			// the final message here and end the stream.
			if (line.includes('[Dockpal Installer] Error:') || line.includes('Installation completed successfully')) {
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
		step = 'installing';
		try {
			await api.post(`/instances/${instanceId}/install`, {
				ssh_host: sshHost.trim(),
				ssh_port: sshPort ?? 22,
				ssh_user: sshUser.trim() || 'root',
				ssh_auth_type: sshAuthType,
				ssh_secret: sshSecret,
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
	<!-- Step 1 — register instance -->
	<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-5">
		<div class="flex items-center gap-2 mb-4">
			<span class="w-6 h-6 rounded-full bg-zinc-800 text-zinc-300 text-xs font-semibold flex items-center justify-center">1</span>
			<h3 class="text-sm font-semibold text-white">Register server</h3>
			{#if instanceId}
				<span class="ml-auto text-xs text-emerald-400">✓ {instanceId}</span>
			{/if}
		</div>

		{#if step === 'form'}
			<form class="space-y-3" onsubmit={(e) => { e.preventDefault(); create(); }}>
				<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
					<div>
						<label for="inst-name" class="block text-xs font-medium text-zinc-400 mb-1">Name</label>
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
					<div class="grid grid-cols-3 gap-3">
						<div class="col-span-2">
							<label for="inst-host" class="block text-xs font-medium text-zinc-400 mb-1">Agent host</label>
							<input
								id="inst-host"
								type="text"
								bind:value={host}
								placeholder="203.0.113.10"
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
				{/if}

				{#if createError}
					<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm text-sm text-red-400">{createError}</div>
				{/if}

				<div class="flex justify-end">
					<Button type="submit" variant="primary" loading={creating} disabled={!canCreate}>Create instance</Button>
				</div>
			</form>
		{:else}
			<p class="text-xs text-zinc-500">
				Mode: <span class="text-zinc-300 uppercase">{mode}</span>
				{#if mode === 'direct'} · {host}:{port}{/if}
			</p>
		{/if}
	</div>

	<!-- Step 2 — install agent (only after create) -->
	{#if instanceId && (step === 'created' || step === 'installing')}
		<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-5">
			<div class="flex items-center gap-2 mb-4">
				<span class="w-6 h-6 rounded-full bg-zinc-800 text-zinc-300 text-xs font-semibold flex items-center justify-center">2</span>
				<h3 class="text-sm font-semibold text-white">Install agent</h3>
				{#if step === 'installing'}
					<span class="ml-auto text-xs text-blue-400 animate-pulse">installing…</span>
				{/if}
			</div>

			<!-- SSH auto-install -->
			<div class="space-y-3">
				<div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
					<div>
						<label for="ssh-host" class="block text-xs font-medium text-zinc-400 mb-1">SSH host</label>
						<input
							id="ssh-host"
							type="text"
							bind:value={sshHost}
							placeholder={mode === 'direct' && host ? host : 'same VPS IP'}
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
				<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
					<div>
						<label for="ssh-auth" class="block text-xs font-medium text-zinc-400 mb-1">Auth type</label>
						<select
							id="ssh-auth"
							bind:value={sshAuthType}
							class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
						>
							<option value="password">Password</option>
							<option value="key">Private key</option>
						</select>
					</div>
					<div>
						<label for="ssh-secret" class="block text-xs font-medium text-zinc-400 mb-1">
							{sshAuthType === 'password' ? 'Password' : 'Private key'}
						</label>
						{#if sshAuthType === 'password'}
							<input
								id="ssh-secret"
								type="password"
								bind:value={sshSecret}
								class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
							/>
						{:else}
							<textarea
								id="ssh-secret"
								bind:value={sshSecret}
								rows="3"
								placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
								class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white font-mono focus:outline-none focus:ring-2 focus:ring-blue-600"
							></textarea>
						{/if}
					</div>
				</div>
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
					SSH credentials are encrypted at rest and used once by the installer — they are not stored in plaintext.
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

	<!-- Step 3 — live logs + verify -->
	{#if step === 'installing' || step === 'done'}
		<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-5">
			<div class="flex items-center gap-2 mb-3">
				<span class="w-6 h-6 rounded-full bg-zinc-800 text-zinc-300 text-xs font-semibold flex items-center justify-center">3</span>
				<h3 class="text-sm font-semibold text-white">Install log</h3>
				{#if step === 'done'}
					<span class="ml-auto text-xs text-emerald-400">session finished</span>
				{/if}
			</div>
			<pre class="p-3 bg-black border border-zinc-800 rounded-sm text-xs text-zinc-300 font-mono whitespace-pre-wrap overflow-auto max-h-64">{logs.join('\n')}</pre>

			{#if step === 'done'}
				<div class="mt-3 flex items-center justify-between gap-3">
					<div class="text-xs">
						{#if testMessage}
							<span class={testOk ? 'text-emerald-400' : 'text-red-400'}>{testMessage}</span>
						{:else}
							<span class="text-zinc-500">Verify the agent connection:</span>
						{/if}
					</div>
					<Button variant="secondary" size="sm" loading={testing} disabled={!canTest} onclick={testConnection}>
						Test connection
					</Button>
				</div>
			{/if}
		</div>
	{/if}

	{#if step === 'done'}
		<div class="flex justify-end">
			<Button variant="secondary" size="sm" onclick={reset}>Add another server</Button>
		</div>
	{/if}
</div>
