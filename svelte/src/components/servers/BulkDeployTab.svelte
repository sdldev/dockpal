<script lang="ts">
	// Bulk-deploy one compose project to many servers at once (operator-only
	// tab on the Servers page). Reads the shared servers store for the target
	// list and for the post-deploy refresh.
	import { api, ApiError } from '$lib/api/client';
	import { addToast, servers, isOperator } from '$lib/store';

	// Bulk deploy form + logs
	let bulkDeployForm = $state({ name: '', compose: '', targets: [] as string[] });
	let bulkDeploying = $state(false);
	let bulkDeployLogs = $state<
		Array<{ timestamp: string; type: 'info' | 'error' | 'success'; message: string; status: string }>
	>([]);

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

	const canBulkDeploy = $derived(
		bulkDeployForm.name.trim() !== '' &&
			bulkDeployForm.compose.trim() !== '' &&
			bulkDeployForm.targets.length > 0 &&
			!bulkDeploying
	);
</script>

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
