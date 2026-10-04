<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { servers, isAdmin, isOperator, addToast, selectedInstance, serverDetailTab } from '../../lib/store';
	import type { ServersInstance } from '../../lib/store';
	import { api } from '../../lib/api/client';
	import { deleteThenToast } from '../../lib/ui-actions';
	import { updateInstance } from '../../lib/api/stacks';
	import { formatBytes, formatPorts } from '$lib/format';
	import { navigate } from '../../lib/router';
	import AddServerPanel from '../servers/AddServerPanel.svelte';
	import ServerSummaryCards from '../servers/ServerSummaryCards.svelte';
	import BulkDeployTab from '../servers/BulkDeployTab.svelte';
	import Icon from '../ui/Icon.svelte';
	import Button from '../ui/Button.svelte';
	import Modal from '../ui/Modal.svelte';
	import ConfirmDialog from '../ui/ConfirmDialog.svelte';

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
		const target = removeTarget;
		removeTarget = null;
		instanceMenuOpen = null;
		await deleteThenToast(`/instances/${target.id}`, `Server "${target.name}" removed`, {
			errorFallback: 'Failed to remove server',
			refresh: servers.fetchMetrics
		});
		removing = false;
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

	onMount(() => {
		// One aggregated fleet-summary request per poll; 15s keeps the page
		// fresh while leaving headroom under the backend's read rate limit.
		servers.startPolling(15000);
	});

	onDestroy(() => {
		servers.stopPolling();
	});

	const filteredContainers = $derived(
		!containerSearch.trim()
			? $servers.containers
			: $servers.containers.filter(
					(c) =>
						c.name.toLowerCase().includes(containerSearch.toLowerCase()) ||
						c.instanceName.toLowerCase().includes(containerSearch.toLowerCase())
				)
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
			<ServerSummaryCards />

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
		<BulkDeployTab />
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
