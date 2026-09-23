<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { createFleetStore, isOperator, addToast } from '../../lib/store';
	import { api, ApiError } from '../../lib/api/client';
	import { formatBytes } from '../../lib/stats-history';
	import { formatPorts } from '../../lib/format';

	const fleet = createFleetStore();

	type Tab = 'overview' | 'containers' | 'bulk-deploy';
	let fleetTab = $state<Tab>('overview');

	let containerSearch = $state('');

	// Bulk deploy form + logs (operator-only tab)
	let bulkDeployForm = $state({ name: '', compose: '', targets: [] as string[] });
	let bulkDeploying = $state(false);
	let bulkDeployLogs = $state<
		Array<{ timestamp: string; type: 'info' | 'error' | 'success'; message: string; status: string }>
	>([]);

	onMount(() => {
		fleet.startPolling(5000);
	});

	onDestroy(() => {
		fleet.stopPolling();
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
				const inst = $fleet.instances.find((i) => i.id === targetId);
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

		await fleet.fetchMetrics();
	}

	function toggleTarget(id: string) {
		bulkDeployForm.targets = bulkDeployForm.targets.includes(id)
			? bulkDeployForm.targets.filter((t) => t !== id)
			: [...bulkDeployForm.targets, id];
	}

	const onlineInstances = $derived($fleet.instances.filter((i) => fleet.isOnline(i)));
	const runningCount = $derived($fleet.containers.filter((c) => c.state === 'running').length);
	const totalCpuCores = $derived(
		$fleet.instances.reduce((acc, inst) => acc + (inst.sysInfo?.cpu_cores || 0), 0)
	);
	const totalMemory = $derived(
		$fleet.instances.reduce((acc, inst) => acc + (inst.sysInfo?.total_ram || 0), 0)
	);
	const onlineCount = $derived(onlineInstances.length);
	const offlineCount = $derived($fleet.instances.length - onlineCount);

	const filteredContainers = $derived(
		!containerSearch.trim()
			? $fleet.containers
			: $fleet.containers.filter(
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

	const tabs: Array<{ id: Tab; label: string; operatorOnly?: boolean }> = [
		{ id: 'overview', label: 'Overview' },
		{ id: 'containers', label: 'All Containers' },
		{ id: 'bulk-deploy', label: 'Bulk Deploy', operatorOnly: true }
	];
</script>

<div class="space-y-6">
	<!-- Page Header -->
	<div class="flex items-center justify-between">
		<div>
			<h2 class="text-xl font-semibold text-white">Fleet Dashboard</h2>
			<p class="text-sm text-zinc-500 mt-0.5">Real-time status and deployment management across all Docker hosts</p>
		</div>
		<div class="flex gap-2">
			{#each tabs as tab}
				{#if !tab.operatorOnly || $isOperator}
					<button
						onclick={() => (fleetTab = tab.id)}
						class={`px-4 py-2 rounded-sm text-sm font-medium transition-all ${fleetTab === tab.id ? 'bg-white text-zinc-900' : 'bg-zinc-900 border border-zinc-800 text-zinc-400 hover:text-white'}`}
					>
						{tab.label}
					</button>
				{/if}
			{/each}
		</div>
	</div>

	{#if $fleet.loading}
		<div class="text-zinc-500 text-sm">Loading fleet...</div>
	{:else if fleetTab === 'overview'}
		<!-- Fleet Overview Tab -->
		<div class="space-y-6">
			<!-- Fleet Summary KPI Cards -->
			<div class="grid grid-cols-1 md:grid-cols-4 gap-4">
				<div class="bg-zinc-900 border border-zinc-800/60 rounded-sm p-5">
					<div class="text-xs text-zinc-500 uppercase tracking-wider mb-1">Total Instances</div>
					<div class="text-3xl font-bold text-white">{$fleet.instances.length}</div>
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
					<div class="text-[10px] text-zinc-500 mt-1">Fleet CPU capacity</div>
				</div>
				<div class="bg-zinc-900 border border-zinc-800/60 rounded-sm p-5">
					<div class="text-xs text-zinc-500 uppercase tracking-wider mb-1">Total Memory Capacity</div>
					<div class="text-3xl font-bold text-violet-400">{formatBytes(totalMemory)}</div>
					<div class="text-[10px] text-zinc-500 mt-1">Fleet RAM capacity</div>
				</div>
			</div>

			<!-- Instance Cards Grid -->
			<div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
				{#each $fleet.instances as inst (inst.id)}
					<div class="bg-zinc-900 border border-zinc-800/80 rounded-sm p-5 flex flex-col justify-between space-y-4">
						<!-- Instance Title / Status -->
						<div class="flex items-center justify-between border-b border-zinc-800/60 pb-3">
							<div>
								<div class="flex items-center gap-2">
									<span
										class={`w-2.5 h-2.5 rounded-full ${fleet.isOnline(inst) ? 'bg-green-500' : 'bg-red-500'}`}
									></span>
									<span class="font-semibold text-white text-sm">
										{inst.id === 'local' ? 'This Server' : inst.name}
									</span>
									<span
										class="text-[10px] bg-zinc-800 border border-zinc-700/60 text-zinc-400 px-1.5 py-0.5 rounded uppercase font-mono"
									>
										{inst.mode || 'local'}
									</span>
								</div>
								<p class="text-xs text-zinc-500 mt-1 font-mono">
									{inst.id === 'local' ? 'Local Connection' : inst.host || 'Edge Agent'}
								</p>
							</div>

							<div class="text-right">
								<span
									class={`text-xs font-semibold px-2 py-0.5 rounded ${fleet.isOnline(inst) ? 'bg-green-500/10 text-green-400' : 'bg-red-500/10 text-red-400'}`}
								>
									{fleet.isOnline(inst) ? 'ONLINE' : 'OFFLINE'}
								</span>
							</div>
						</div>

						<!-- Gauges (only if online) -->
						{#if fleet.isOnline(inst)}
							<div class="grid grid-cols-3 gap-3">
								<!-- CPU -->
								<div class="space-y-1.5">
									<div class="flex justify-between text-xs text-zinc-400">
										<span>CPU</span>
										<span class="font-semibold">{(inst.sysInfo?.cpu_percent || 0).toFixed(1)}%</span>
									</div>
									<div class="w-full bg-zinc-800 rounded-full h-1.5">
										<div
											class="bg-blue-500 h-1.5 rounded-full"
											style={`width: ${Math.min(inst.sysInfo?.cpu_percent || 0, 100)}%`}
										></div>
									</div>
								</div>

								<!-- RAM -->
								<div class="space-y-1.5">
									<div class="flex justify-between text-xs text-zinc-400">
										<span>Memory</span>
										<span class="font-semibold"
											>{gaugePercent(inst.sysInfo?.used_ram || 0, inst.sysInfo?.total_ram || 0).toFixed(
												1
											)}%</span
										>
									</div>
									<div class="w-full bg-zinc-800 rounded-full h-1.5">
										<div
											class="bg-emerald-500 h-1.5 rounded-full"
											style={`width: ${gaugePercent(inst.sysInfo?.used_ram || 0, inst.sysInfo?.total_ram || 0)}%`}
										></div>
									</div>
								</div>

								<!-- Disk -->
								<div class="space-y-1.5">
									<div class="flex justify-between text-xs text-zinc-400">
										<span>Disk</span>
										<span class="font-semibold"
											>{gaugePercent(inst.sysInfo?.used_disk || 0, inst.sysInfo?.total_disk || 0).toFixed(
												1
											)}%</span
										>
									</div>
									<div class="w-full bg-zinc-800 rounded-full h-1.5">
										<div
											class="bg-violet-500 h-1.5 rounded-full"
											style={`width: ${gaugePercent(inst.sysInfo?.used_disk || 0, inst.sysInfo?.total_disk || 0)}%`}
										></div>
									</div>
								</div>
							</div>
						{:else}
							<div class="py-4 text-center text-xs text-zinc-500">
								Agent is unreachable. Ensure the agent service is running on the host.
							</div>
						{/if}

						<!-- Containers List Summary -->
						<div class="border-t border-zinc-800/60 pt-3">
							<div class="flex justify-between text-xs text-zinc-400 mb-2">
								<span>Containers</span>
								<span class="font-semibold">{inst.containers?.length || 0}</span>
							</div>

							<div class="flex flex-wrap gap-1.5 max-h-24 overflow-y-auto pr-1">
								{#each inst.containers || [] as c (c.id)}
									<span
										class="px-2 py-0.5 bg-zinc-950 border border-zinc-800 rounded-full text-[10px] text-zinc-300 flex items-center gap-1.5"
									>
										<span
											class={`w-1.5 h-1.5 rounded-full ${c.state === 'running' ? 'bg-green-500' : 'bg-zinc-600'}`}
										></span>
										<span>{c.name}</span>
									</span>
								{/each}
								{#if !inst.containers || inst.containers.length === 0}
									<span class="text-xs text-zinc-600">No containers</span>
								{/if}
							</div>
						</div>
					</div>
				{/each}
			</div>
		</div>
	{:else if fleetTab === 'containers'}
		<!-- Fleet Containers Tab -->
		<div class="bg-zinc-900 border border-zinc-800 rounded-sm overflow-hidden">
			<div class="p-4 border-b border-zinc-800 flex items-center justify-between">
				<h3 class="text-sm font-semibold text-white">All Running Containers Across Fleet</h3>
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
								{containerSearch ? 'No containers match your search.' : 'No containers found across the fleet.'}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{:else if fleetTab === 'bulk-deploy'}
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
									placeholder="my-fleet-app"
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
								{#each $fleet.instances as inst (inst.id)}
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
											disabled={!fleet.isOnline(inst)}
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
											class={`text-xs ${fleet.isOnline(inst) ? 'text-green-400' : 'text-red-400'}`}
										>
											{fleet.isOnline(inst) ? 'Online' : 'Offline'}
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
							<span>{bulkDeploying ? 'Deploying to Fleet...' : 'Deploy to Selected Targets'}</span>
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
	{/if}
</div>
