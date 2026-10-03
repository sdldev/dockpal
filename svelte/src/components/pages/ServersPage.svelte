<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { createServersStore, isAdmin, isOperator, addToast, selectedInstance, serverDetailTab } from '../../lib/store';
	import type { ServersInstance } from '../../lib/store';
	import { api, ApiError } from '../../lib/api/client';
	import { updateInstance } from '../../lib/api/stacks';
	import { formatBytes } from '../../lib/stats-history';
	import { formatPorts } from '../../lib/format';
	import { navigate } from '../../lib/router';
	import AddServerPanel from '../servers/AddServerPanel.svelte';
	import Icon from '../ui/Icon.svelte';
	import Button from '../ui/Button.svelte';
	import Modal from '../ui/Modal.svelte';
	import ConfirmDialog from '../ui/ConfirmDialog.svelte';

	const servers = createServersStore();

	type Tab = 'overview' | 'containers' | 'bulk-deploy' | 'add-server';
	let serversTab = $state<Tab>('overview');

	let containerSearch = $state('');

	// Instance card actions (admin-only): edit + remove + test connection.
	let instanceMenuOpen = $state<string | null>(null);
	let removeTarget = $state<{ id: string; name: string } | null>(null);
	let removing = $state(false);
	let testingId = $state<string | null>(null);
	let testResult = $state<{ id: string; ok: boolean; message: string } | null>(null);

	// Security badge for the Servers table: detected state wins (it reflects
	// the server's REAL sshd config), then the panel's hardening record, then
	// the login type. Unknown detection = not verified = not green.
	function securityBadge(inst: ServersInstance): { label: string; cls: string; clickable: boolean } {
		if (inst.id === 'local') return { label: 'Local', cls: 'bg-zinc-800 text-zinc-500', clickable: false };
		if (inst.sec_password_auth === 'no')
			return { label: 'Hardened', cls: 'bg-green-500/10 text-green-400', clickable: true };
		if (inst.sec_password_auth === 'yes') {
			if (inst.ssh_auth_type === 'key')
				return { label: 'Key auth', cls: 'bg-blue-500/10 text-blue-400', clickable: true };
			return { label: 'Password auth', cls: 'bg-amber-500/10 text-amber-400', clickable: true };
		}
		if (inst.sec_password_auth === 'unknown' && inst.sec_checked_at)
			return { label: 'Unreachable', cls: 'bg-red-500/10 text-red-400', clickable: true };
		// No detection yet — fall back to the stored record.
		if (inst.ssh_hardening_status === 'hardened')
			return { label: 'Hardened', cls: 'bg-green-500/10 text-green-400', clickable: true };
		if (inst.ssh_auth_type === 'key')
			return { label: 'Key auth', cls: 'bg-blue-500/10 text-blue-400', clickable: true };
		if (inst.ssh_auth_type === 'password')
			return { label: 'Password auth', cls: 'bg-amber-500/10 text-amber-400', clickable: true };
		return { label: '—', cls: 'text-zinc-600', clickable: false };
	}

	// Edit (rename) modal state.
	let editTarget = $state<{ id: string; name: string; host: string; port: number } | null>(null);
	let editName = $state('');
	let editSaving = $state(false);
	let editError = $state('');

	function canManage(id: string): boolean {
		return $isAdmin && id !== 'local';
	}

	// Open a server's control panel (/servers/:id): make it the active
	// instance (persisted, so Stacks/Containers follow) and navigate.
	function openServer(id: string) {
		selectedInstance.set(id);
		localStorage.setItem('dockpal_selected_instance', id);
		navigate('server-detail', { id });
	}

	// Jump straight to the detail page's Security tab — the hardening /
	// fail2ban / firewall controls live there now (the old SSH-hardening
	// modal was removed with that move).
	function openSecurity(id: string) {
		serverDetailTab.set('security');
		openServer(id);
	}

	function openEdit(inst: { id: string; name: string; host?: string; port?: number }) {
		editTarget = { id: inst.id, name: inst.name, host: inst.host ?? '', port: inst.port ?? 0 };
		editName = inst.name;
		editError = '';
		instanceMenuOpen = null;
	}

	async function saveEdit() {
		if (!editTarget) return;
		const name = editName.trim();
		if (!name) {
			editError = 'Name is required';
			return;
		}
		editSaving = true;
		editError = '';
		try {
			await updateInstance(editTarget.id, { name });
			addToast(`Server renamed to "${name}"`, 'success');
			editTarget = null;
			await servers.fetchMetrics();
		} catch (e) {
			editError = e instanceof Error ? e.message : 'Failed to update server';
		} finally {
			editSaving = false;
		}
	}

	async function removeInstance() {
		if (!removeTarget) return;
		removing = true;
		try {
			await api.delete(`/instances/${removeTarget.id}`);
			addToast(`Server "${removeTarget.name}" removed`, 'success');
			removeTarget = null;
			instanceMenuOpen = null;
			await servers.fetchMetrics();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Failed to remove server', 'error');
		} finally {
			removing = false;
		}
	}

	async function testInstance(id: string) {
		testingId = id;
		testResult = null;
		try {
			const res = await api.post<{ status: string; message: string }>(`/instances/${id}/test`);
			testResult = { id, ok: res.status === 'ok', message: res.message };
		} catch (e) {
			testResult = { id, ok: false, message: e instanceof Error ? e.message : 'Test failed' };
		} finally {
			testingId = null;
			instanceMenuOpen = null;
		}
	}

	// Bulk deploy form + logs (operator-only tab)
	let bulkDeployForm = $state({ name: '', compose: '', targets: [] as string[] });
	let bulkDeploying = $state(false);
	let bulkDeployLogs = $state<
		Array<{ timestamp: string; type: 'info' | 'error' | 'success'; message: string; status: string }>
	>([]);

	onMount(() => {
		// Poll at 15s, not 5s: fetchMetrics issues 3 requests per instance
		// (system/info + containers + images), so a 5s cadence with a few
		// instances already exceeds the backend's 60 req/min read limit and the
		// gauges start rendering zeros from 429s. 15s keeps the page fresh
		// while staying well under the limit.
		servers.startPolling(15000);
	});

	onDestroy(() => {
		servers.stopPolling();
	});

	const now = () => new Date().toLocaleTimeString();

	function addBulkDeployLog(
		timestamp: string,
		type: 'info' | 'error' | 'success',
		message: string,
		status: string
	) {
		bulkDeployLogs = [...bulkDeployLogs, { timestamp, type, message, status }];
	}

	async function executeBulkDeploy() {
		const targets = bulkDeployForm.targets;
		if (targets.length === 0) {
			addToast('Please select at least one deployment target', 'error');
			return;
		}

		bulkDeploying = true;
		bulkDeployLogs = [];

		const { name, compose } = bulkDeployForm;
		addBulkDeployLog(now(), 'info', `Starting bulk deployment of "${name}" to ${targets.length} targets...`, 'running');

		const results = await Promise.all(
			targets.map(async (targetId) => {
				const inst = $servers.instances.find((i) => i.id === targetId);
				const displayName = inst ? (inst.id === 'local' ? 'This Server' : inst.name) : targetId;

				addBulkDeployLog(now(), 'info', `Deploying to ${displayName}...`, 'running');

				try {
					await api.post(`/instances/${targetId}/deploy/compose`, { name, compose });
					addBulkDeployLog(now(), 'success', `Deployment to ${displayName} succeeded.`, 'success');
					return 'success';
				} catch (e) {
					const msg = e instanceof ApiError ? e.message : 'Connection error';
					addBulkDeployLog(now(), 'error', `Deployment to ${displayName} failed: ${msg}`, 'failed');
					return 'failed';
				}
			})
		);

		const succeeded = results.filter((r) => r === 'success').length;
		const failed = results.length - succeeded;
		addBulkDeployLog(
			now(),
			succeeded > 0 ? 'success' : 'error',
			`Bulk deployment completed: ${succeeded} succeeded, ${failed} failed.`,
			succeeded > 0 ? 'success' : 'failed'
		);
		addToast(`Bulk deployment done: ${succeeded} succeeded, ${failed} failed`, succeeded > 0 ? 'success' : 'error');

		bulkDeployForm = { name: '', compose: '', targets: [] };
		bulkDeploying = false;

		await servers.fetchMetrics();
	}

	function toggleTarget(id: string) {
		bulkDeployForm.targets = bulkDeployForm.targets.includes(id)
			? bulkDeployForm.targets.filter((t) => t !== id)
			: [...bulkDeployForm.targets, id];
	}

	const onlineInstances = $derived($servers.instances.filter((i) => servers.isOnline(i)));
	const runningCount = $derived($servers.containers.filter((c) => c.state === 'running').length);
	const totalCpuCores = $derived(
		$servers.instances.reduce((acc, inst) => acc + (inst.sysInfo?.cpu_cores || 0), 0)
	);
	const totalMemory = $derived(
		$servers.instances.reduce((acc, inst) => acc + (inst.sysInfo?.total_ram || 0), 0)
	);
	const onlineCount = $derived(onlineInstances.length);
	const offlineCount = $derived($servers.instances.length - onlineCount);

	const filteredContainers = $derived(
		!containerSearch.trim()
			? $servers.containers
			: $servers.containers.filter(
					(c) =>
						c.name.toLowerCase().includes(containerSearch.toLowerCase()) ||
						c.instanceName.toLowerCase().includes(containerSearch.toLowerCase())
				)
	);

	const canBulkDeploy = $derived(
		bulkDeployForm.name.trim() !== '' &&
			bulkDeployForm.compose.trim() !== '' &&
			bulkDeployForm.targets.length > 0 &&
			!bulkDeploying
	);

	function gaugePercent(value: number, total: number): number {
		return total > 0 ? (value / total) * 100 : 0;
	}

	const tabs: Array<{ id: Tab; label: string; operatorOnly?: boolean; adminOnly?: boolean }> = [
		{ id: 'overview', label: 'Overview' },
		{ id: 'containers', label: 'All Containers' },
		{ id: 'bulk-deploy', label: 'Bulk Deploy', operatorOnly: true },
		{ id: 'add-server', label: '+ Add Server', adminOnly: true }
	];
</script>

{#snippet gauge(pct: number, barClass: string, label: string)}
	<div class="min-w-24 max-w-36 space-y-1">
		<div class="flex justify-between text-[11px] text-zinc-400">
			<span class="font-semibold text-zinc-300">{pct.toFixed(1)}%</span>
		</div>
		<div class="w-full bg-zinc-800 rounded-full h-1.5">
			<div class={`${barClass} h-1.5 rounded-full`} style={`width: ${Math.min(pct, 100)}%`}></div>
		</div>
		<span class="text-[10px] text-zinc-600 font-mono">{label}</span>
	</div>
{/snippet}

{#snippet securityBadgeCell(inst: ServersInstance)}
	{@const badge = securityBadge(inst)}
	{#if badge.clickable}
		<button
			class={`text-xs font-semibold px-2 py-0.5 rounded ${badge.cls} hover:brightness-125 transition-all`}
			title="Open the server's security controls"
			onclick={() => openSecurity(inst.id)}
		>
			{badge.label}
		</button>
	{:else}
		<span class={`text-xs font-semibold px-2 py-0.5 rounded ${badge.cls}`}>{badge.label}</span>
	{/if}
{/snippet}

<div class="space-y-6">
	<!-- Tabs (page title lives in the navheader now) -->
	<div class="flex items-center justify-end">
		<div class="flex gap-2">
			{#each tabs as tab}
				{#if (!tab.operatorOnly || $isOperator) && (!tab.adminOnly || $isAdmin)}
					<button
						onclick={() => (serversTab = tab.id)}
						class={`px-4 py-2 rounded-sm text-sm font-medium transition-all ${serversTab === tab.id ? 'bg-white text-zinc-900' : 'bg-zinc-900 border border-zinc-800 text-zinc-400 hover:text-white'}`}
					>
						{tab.label}
					</button>
				{/if}
			{/each}
		</div>
	</div>

	{#if $servers.loading}
		<div class="text-zinc-500 text-sm">Loading servers...</div>
	{:else if serversTab === 'overview'}
		<!-- Servers Overview Tab -->
		<div class="space-y-6">
			<!-- Servers Summary KPI Cards -->
			<div class="grid grid-cols-1 md:grid-cols-4 gap-4">
				<div class="bg-zinc-900 border border-zinc-800/60 rounded-sm p-5">
					<div class="text-xs text-zinc-500 uppercase tracking-wider mb-1">Total Instances</div>
					<div class="text-3xl font-bold text-white">{$servers.instances.length}</div>
					<div class="text-[10px] text-zinc-500 mt-1">{onlineCount} Online / {offlineCount} Offline</div>
				</div>
				<div class="bg-zinc-900 border border-zinc-800/60 rounded-sm p-5">
					<div class="text-xs text-zinc-500 uppercase tracking-wider mb-1">Total Running Containers</div>
					<div class="text-3xl font-bold text-emerald-400">{runningCount}</div>
					<div class="text-[10px] text-zinc-500 mt-1">Across all online agents</div>
				</div>
				<div class="bg-zinc-900 border border-zinc-800/60 rounded-sm p-5">
					<div class="text-xs text-zinc-500 uppercase tracking-wider mb-1">Total CPU Cores</div>
					<div class="text-3xl font-bold text-blue-400">{totalCpuCores}</div>
					<div class="text-[10px] text-zinc-500 mt-1">Total CPU capacity</div>
				</div>
				<div class="bg-zinc-900 border border-zinc-800/60 rounded-sm p-5">
					<div class="text-xs text-zinc-500 uppercase tracking-wider mb-1">Total Memory Capacity</div>
					<div class="text-3xl font-bold text-violet-400">{formatBytes(totalMemory)}</div>
					<div class="text-[10px] text-zinc-500 mt-1">Total RAM capacity</div>
				</div>
			</div>

			<!-- Servers table: per-server resource + workload summary.
			     Clip X only: the row actions dropdown must escape the card
			     vertically (overflow-y:hidden would cut it off at the edge). -->
			<div class="bg-zinc-900 border border-zinc-800 rounded-sm overflow-x-clip overflow-y-visible">
				<table class="w-full">
					<thead>
							<tr class="border-b border-zinc-800 bg-zinc-950/20">
								<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Name</th>
								<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Status</th>
								<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Security</th>
								<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">CPU</th>
							<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Memory</th>
							<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Disk</th>
							<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Containers</th>
							<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Images</th>
							<th class="text-right px-4 py-2.5 text-xs font-medium text-zinc-500"></th>
						</tr>
					</thead>
					<tbody>
						{#each $servers.instances as inst (inst.id)}
							{@const online = servers.isOnline(inst)}
							{@const running = (inst.containers ?? []).filter((c) => c.state === 'running').length}
							{@const stopped = (inst.containers ?? []).length - running}
							<tr class="border-b border-zinc-800/40 hover:bg-zinc-950/10">
								<!-- Name / mode / host -->
								<td class="px-4 py-3">
									<button
										class="flex items-center gap-2 text-left hover:bg-zinc-800/40 -mx-2 -my-1.5 px-2 py-1.5 rounded transition-colors group"
										title={`Open ${inst.id === 'local' ? 'This Server' : inst.name} control panel`}
										onclick={() => openServer(inst.id)}
									>
										<span
											class={`w-2.5 h-2.5 rounded-full shrink-0 ${online ? 'bg-green-500' : 'bg-red-500'}`}
										></span>
										<div>
											<div class="flex items-center gap-2">
												<span class="text-sm font-semibold text-white group-hover:text-sky-300 group-hover:underline transition-colors">
													{inst.id === 'local' ? 'This Server' : inst.name}
												</span>
												<span
													class="text-[10px] bg-zinc-800 border border-zinc-700/60 text-zinc-400 px-1.5 py-0.5 rounded uppercase font-mono"
												>
													{inst.mode || 'local'}
												</span>
											</div>
											<span class="text-xs text-zinc-500 font-mono">
												{inst.id === 'local' ? 'Local Connection' : inst.host || 'Edge Agent'}
											</span>
										</div>
									</button>
								</td>

								<!-- Status -->
								<td class="px-4 py-3">
									<span
										class={`text-xs font-semibold px-2 py-0.5 rounded ${online ? 'bg-green-500/10 text-green-400' : 'bg-red-500/10 text-red-400'}`}
									>
										{online ? 'ONLINE' : 'OFFLINE'}
									</span>
									{#if testResult && testResult.id === inst.id}
										<div class={`mt-1.5 text-[11px] ${testResult.ok ? 'text-emerald-400' : 'text-red-400'}`}>
											{testResult.ok ? '✓' : '✕'} {testResult.message}
										</div>
									{/if}
								</td>

	<!-- Security (SSH hardening) -->
	<td class="px-4 py-3">
		{@render securityBadgeCell(inst)}
	</td>

	<!-- CPU / Memory / Disk -->
	{#if online}
									<td class="px-4 py-3">
										{@render gauge(inst.sysInfo?.cpu_percent || 0, 'bg-blue-500', `${inst.sysInfo?.cpu_cores ?? 0} cores`)}
									</td>
									<td class="px-4 py-3">
										{@render gauge(gaugePercent(inst.sysInfo?.used_ram || 0, inst.sysInfo?.total_ram || 0), 'bg-emerald-500', `${formatBytes(inst.sysInfo?.used_ram || 0)} / ${formatBytes(inst.sysInfo?.total_ram || 0)}`)}
									</td>
									<td class="px-4 py-3">
										{@render gauge(gaugePercent(inst.sysInfo?.used_disk || 0, inst.sysInfo?.total_disk || 0), 'bg-violet-500', `${formatBytes(inst.sysInfo?.used_disk || 0)} / ${formatBytes(inst.sysInfo?.total_disk || 0)}`)}
									</td>
								{:else}
									<td colspan="3" class="px-4 py-3 text-xs text-zinc-600">Agent unreachable</td>
								{/if}

								<!-- Containers (on/off) -->
								<td class="px-4 py-3">
									{#if online}
										<div class="flex items-center gap-1.5 text-xs">
											<span class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-emerald-400/10 text-emerald-400 font-medium">
												<span class="w-1.5 h-1.5 rounded-full bg-emerald-400"></span>{running}
											</span>
											<span class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-zinc-400/10 text-zinc-400 font-medium">
												<span class="w-1.5 h-1.5 rounded-full bg-zinc-500"></span>{stopped}
											</span>
										</div>
									{:else}
										<span class="text-xs text-zinc-600">—</span>
									{/if}
								</td>

								<!-- Images -->
								<td class="px-4 py-3 text-sm text-zinc-300">
									{online ? inst.imageCount : '—'}
								</td>

								<!-- Actions -->
								<td class="px-4 py-3 text-right">
									{#if canManage(inst.id)}
										<div class="relative inline-block text-left">
											<button
												class="p-1.5 rounded-sm text-zinc-500 hover:text-white hover:bg-zinc-800 transition-colors"
												title="Server actions"
												aria-label="Server actions for {inst.id === 'local' ? 'This Server' : inst.name}"
												onclick={() => (instanceMenuOpen = instanceMenuOpen === inst.id ? null : inst.id)}
											>
												<Icon name="settings" class="w-4 h-4" />
											</button>
											{#if instanceMenuOpen === inst.id}
												<button
													class="fixed inset-0 z-40 cursor-default"
													aria-label="Close menu"
													onclick={() => (instanceMenuOpen = null)}
												></button>
												<div class="absolute right-0 top-9 z-50 w-44 bg-zinc-900 border border-zinc-700 rounded-sm shadow-lg py-1 text-left">
													<button
														class="w-full text-left px-3 py-2 text-sm text-zinc-300 hover:bg-zinc-800 hover:text-white flex items-center gap-2"
														onclick={() => openEdit(inst)}
													>
														<Icon name="settings" class="w-4 h-4" />
														Edit server
													</button>
													<button
														class="w-full text-left px-3 py-2 text-sm text-zinc-300 hover:bg-zinc-800 hover:text-white flex items-center gap-2"
														onclick={() => { instanceMenuOpen = null; openSecurity(inst.id); }}
													>
														<Icon name="admin" class="w-4 h-4" />
														Security
													</button>
													<button
														class="w-full text-left px-3 py-2 text-sm text-zinc-300 hover:bg-zinc-800 hover:text-white flex items-center gap-2"
														disabled={testingId === inst.id}
														onclick={() => testInstance(inst.id)}
													>
														<Icon name="restart" class="w-4 h-4" />
														{testingId === inst.id ? 'Testing…' : 'Test connection'}
													</button>
													<button
														class="w-full text-left px-3 py-2 text-sm text-red-400 hover:bg-zinc-800 hover:text-red-300 flex items-center gap-2"
														onclick={() => { removeTarget = { id: inst.id, name: inst.id === 'local' ? 'This Server' : inst.name }; }}
													>
														<Icon name="trash" class="w-4 h-4" />
														Remove server
													</button>
												</div>
											{/if}
										</div>
									{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		</div>
	{:else if serversTab === 'containers'}
		<!-- All Containers Tab -->
		<div class="bg-zinc-900 border border-zinc-800 rounded-sm overflow-hidden">
			<div class="p-4 border-b border-zinc-800 flex items-center justify-between">
				<h3 class="text-sm font-semibold text-white">All Running Containers Across Servers</h3>
				<div class="flex gap-2">
					<input
						type="text"
						bind:value={containerSearch}
						placeholder="Search containers..."
						class="px-3 py-1.5 bg-zinc-950 border border-zinc-800 rounded-sm text-xs text-white focus:outline-none focus:ring-1 focus:ring-blue-600"
					/>
				</div>
			</div>

			<table class="w-full">
				<thead>
					<tr class="border-b border-zinc-800 bg-zinc-950/20">
						<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Name</th>
						<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Instance</th>
						<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Image</th>
						<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Status</th>
						<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500 hidden sm:table-cell">Ports</th>
					</tr>
				</thead>
				<tbody>
					{#each filteredContainers as c (c.instanceId + '-' + c.id)}
						<tr class="border-b border-zinc-800/40 hover:bg-zinc-950/10">
							<td class="px-4 py-2.5 text-sm font-medium text-white">
								<span class="flex items-center gap-2">
									<span
										class={`w-2 h-2 rounded-full ${c.state === 'running' ? 'bg-green-500' : 'bg-red-500'}`}
									></span>
									{c.name}
								</span>
							</td>
							<td class="px-4 py-2.5 text-sm text-zinc-400">{c.instanceName}</td>
							<td class="px-4 py-2.5 text-sm text-zinc-500 font-mono truncate max-w-xs">{c.image}</td>
							<td class="px-4 py-2.5 text-sm text-zinc-400">{c.status}</td>
							<td class="px-4 py-2.5 text-sm text-zinc-500 hidden sm:table-cell">
									{formatPorts(c.ports)}
							</td>
						</tr>
					{:else}
						<tr>
							<td colspan="5" class="text-center py-10 text-zinc-600 text-sm">
								{containerSearch ? 'No containers match your search.' : 'No containers found across servers.'}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{:else if serversTab === 'bulk-deploy'}
		<!-- Bulk Deploy Tab -->
		{#if $isOperator}
			<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-5 space-y-4">
				<div>
					<h3 class="text-base font-semibold text-white">Bulk Application Deployment</h3>
					<p class="text-xs text-zinc-500 mt-1">
						Deploy a Docker Compose application to multiple target instances simultaneously.
					</p>
				</div>

				<form onsubmit={(e) => { e.preventDefault(); executeBulkDeploy(); }} class="space-y-4">
					<div class="grid grid-cols-1 md:grid-cols-2 gap-4">
						<!-- Input Fields -->
						<div class="space-y-4">
							<div class="space-y-1.5">
								<label class="text-xs text-zinc-400" for="bulk-app-name">Application Name</label>
								<input
									id="bulk-app-name"
									type="text"
									bind:value={bulkDeployForm.name}
									required
									placeholder="my-server-app"
									class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
								/>
							</div>

							<div class="space-y-1.5">
								<label class="text-xs text-zinc-400" for="bulk-compose">Compose YAML</label>
								<textarea
									id="bulk-compose"
									bind:value={bulkDeployForm.compose}
									rows="10"
									required
									placeholder={'services:\n  web:\n    image: nginx:alpine\n    ports:\n      - "8080:80"'}
									class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white font-mono placeholder:text-zinc-600 focus:outline-none focus:ring-2 focus:ring-blue-600 resize-none"
								></textarea>
							</div>
						</div>

						<!-- Target Instances Checkboxes -->
						<div class="space-y-3 bg-zinc-950 border border-zinc-800/60 rounded-sm p-4">
							<span class="text-xs font-semibold text-zinc-400 uppercase tracking-wider"
								>Select Deployment Targets</span
							>
							<div class="space-y-2 max-h-64 overflow-y-auto">
								{#each $servers.instances as inst (inst.id)}
									<div
										role="button"
										tabindex="0"
										onclick={() => toggleTarget(inst.id)}
										onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggleTarget(inst.id); } }}
										class="flex items-center gap-3 p-2 hover:bg-zinc-900 rounded cursor-pointer"
									>
										<input
											type="checkbox"
											checked={bulkDeployForm.targets.includes(inst.id)}
											disabled={!servers.isOnline(inst)}
											class="rounded bg-zinc-900 border-zinc-800 text-blue-600 focus:ring-blue-600 pointer-events-none"
										/>
										<div class="flex-1 min-w-0">
											<div class="flex items-center gap-1.5">
												<span class="text-sm font-medium text-white">
													{inst.id === 'local' ? 'This Server' : inst.name}
												</span>
												<span
													class="text-[10px] px-1.5 py-0.2 bg-zinc-800 text-zinc-500 rounded font-mono">
													{inst.mode || 'local'}
												</span>
											</div>
											<span class="text-xs text-zinc-500 truncate">{inst.host || 'Local Daemon'}</span>
										</div>
										<span
											class={`text-xs ${servers.isOnline(inst) ? 'text-green-400' : 'text-red-400'}`}
										>
											{servers.isOnline(inst) ? 'Online' : 'Offline'}
										</span>
									</div>
								{/each}
							</div>
						</div>
					</div>

					<!-- Action Button -->
					<div class="flex justify-end pt-2 border-t border-zinc-800/60">
						<button
							type="submit"
							disabled={!canBulkDeploy}
							class="px-6 py-2.5 bg-white hover:bg-zinc-200 disabled:opacity-50 text-zinc-900 rounded-sm text-sm font-medium flex items-center gap-2"
						>
							{#if bulkDeploying}
								<svg class="w-4 h-4 animate-spin" fill="none" viewBox="0 0 24 24">
									<circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
									<path
										class="opacity-75"
										fill="currentColor"
										d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
									/>
								</svg>
							{/if}
							<span>{bulkDeploying ? 'Deploying to servers...' : 'Deploy to Selected Servers'}</span>
						</button>
					</div>
				</form>

				<!-- Deployment Status Logs -->
				{#if bulkDeployLogs.length > 0}
					<div class="mt-4 space-y-2">
						<h4 class="text-xs font-semibold text-zinc-400 uppercase tracking-wider">Deployment Status Log</h4>
						<div
							class="bg-black/50 border border-zinc-800 rounded-sm p-4 space-y-1.5 font-mono text-xs max-h-60 overflow-y-auto"
						>
							{#each bulkDeployLogs as log}
								<div class="flex justify-between items-start gap-4">
									<span class="text-zinc-500">{log.timestamp}</span>
									<span
										class={`flex-1 ${log.type === 'error' ? 'text-red-400' : log.type === 'success' ? 'text-green-400' : 'text-blue-400'}`}
									>
										{log.message}
									</span>
									<span
										class={`text-[10px] uppercase font-semibold px-1.5 py-0.2 rounded ${log.status === 'success' ? 'bg-green-500/10 text-green-400' : log.status === 'failed' ? 'bg-red-500/10 text-red-400' : 'bg-blue-500/10 text-blue-400'}`}
									>
										{log.status}
									</span>
								</div>
							{/each}
						</div>
					</div>
				{/if}
			</div>
		{:else}
			<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-5">
				<p class="text-sm text-zinc-500">Bulk deployment requires an operator or admin role.</p>
			</div>
		{/if}
	{:else if serversTab === 'add-server'}
		<!-- Add Server Tab (admin only) -->
		<div class="space-y-4">
			<p class="text-sm text-zinc-500">
				Register a remote Docker host and install the dockpal-agent on it. Once the agent
				connects, the server appears in the selector and this page.
			</p>
			<AddServerPanel />
		</div>
	{/if}
</div>

<ConfirmDialog
	open={removeTarget !== null}
	title="Remove server"
	message={`Remove server "${removeTarget?.name ?? ''}" from Dockpal? This disconnects the agent and deletes the instance record. Containers on that server keep running, but it will no longer be managed or monitored from here. This cannot be undone.`}
	confirmLabel="Remove"
	busy={removing}
	onconfirm={removeInstance}
	onclose={() => (removeTarget = null)}
/>

<Modal open={editTarget !== null} title={`Edit server — ${editTarget?.name ?? ''}`} size="sm" onclose={() => (editTarget = null)}>
	<form class="space-y-3" onsubmit={(e) => { e.preventDefault(); saveEdit(); }}>
		<div>
			<label for="edit-name" class="block text-xs font-medium text-zinc-400 mb-1">Display name</label>
			<input
				id="edit-name"
				type="text"
				bind:value={editName}
				placeholder="server name"
				class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
			/>
		</div>
		{#if editTarget?.host}
			<p class="text-xs text-zinc-600">
				Endpoint: <span class="text-zinc-400 font-mono">{editTarget.host}{editTarget.port ? `:${editTarget.port}` : ''}</span>
				(change it by removing and re-adding the server)
			</p>
		{/if}
		{#if editError}
			<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm text-sm text-red-400">{editError}</div>
		{/if}
		<div class="flex justify-end gap-2 pt-1">
			<Button variant="secondary" size="sm" type="button" onclick={() => (editTarget = null)}>Cancel</Button>
			<Button variant="primary" size="sm" type="submit" loading={editSaving} disabled={!editName.trim()}>Save</Button>
		</div>
	</form>
</Modal>
