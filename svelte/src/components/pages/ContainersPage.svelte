<script lang="ts">
	import { api } from '$lib/api/client';
	import { containersBasePath } from '$lib/api/containers';
	import type { ContainerInfo } from '$lib/types/api';
	import { formatPort, dedupePorts } from '$lib/format';
	import { deleteThenToast } from '$lib/ui-actions';
	import { navigate } from '$lib/router';
	import Button from '../ui/Button.svelte';
	import Icon from '../ui/Icon.svelte';
	import ConfirmDialog from '../ui/ConfirmDialog.svelte';
	import { addToast, selectedInstance, isOperator, containersInitialTab, currentStackName } from '$lib/store';
	import ImagesPage from './ImagesPage.svelte';
	import { get } from 'svelte/store';
	import { onMount } from 'svelte';

	// A container belongs to a stack when compose labelled it. Standard compose
	// (Dockge-style stacks) sets com.docker.compose.project; template deploys
	// set dockpal.project. Either way the value is the stack/project name.
	function stackOf(c: ContainerInfo): string | null {
		const l = c.labels;
		if (!l) return null;
		return l['com.docker.compose.project'] || l['dockpal.project'] || null;
	}

	function openStack(name: string) {
		currentStackName.set(name);
		navigate('compose');
	}

	// Images live here as a tab (infra view), keeping the sidebar focused.
	const tabs = ['containers', 'images'] as const;
	type Tab = (typeof tabs)[number];
	let activeTab = $state<Tab>('containers');

	// Honor the legacy `/images` alias: when the router flagged that we arrived
	// via it, open on the Images tab. Consumed once and cleared so manual tab
	// switches afterwards are unaffected.
	onMount(() => {
		const initial = get(containersInitialTab);
		if (initial) {
			activeTab = initial;
			containersInitialTab.set(null);
		}
	});

	// Instance-aware: list and mutate containers on the host picked in the
	// sidebar, not always the local one (audit-stack-container C2). Recomputed
	// reactively so switching instance re-targets every call below.
	const basePath = $derived(containersBasePath($selectedInstance || 'local'));

	let containers: ContainerInfo[] = $state([]);
	let loading = $state(true);
	let error = $state('');
	let actionBusy = $state<string | null>(null);
	let pendingDelete: ContainerInfo | null = $state(null);

	async function loadContainers() {
		loading = true;
		error = '';
		try {
			containers = await api.get<ContainerInfo[]>(basePath);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load containers';
			containers = [];
		} finally {
			loading = false;
		}
	}

	// Load on mount and again whenever the sidebar instance changes ($effect
	// tracks basePath, which derives from $selectedInstance).
	$effect(() => {
		void basePath;
		loadContainers();
	});

	async function runAction(action: 'start' | 'stop' | 'restart', id: string) {
		actionBusy = id;
		try {
			// api.post already prepends /api — a leading /api here would request /api/api/...
			await api.post(`${basePath}/${encodeURIComponent(id)}/${action}`);
			// Refresh list
			const updated = await api.get<ContainerInfo[]>(basePath);
			containers = updated.map(c => c.id === id ? { ...c, state: action === 'stop' ? 'exited' : 'running' } : c);
		} catch (e) {
			addToast(e instanceof Error ? e.message : `Failed to ${action}`, 'error');
		} finally {
			actionBusy = null;
		}
	}

	async function confirmDelete() {
		const target = pendingDelete;
		if (!target) return;
		actionBusy = target.id;
		await deleteThenToast(`${basePath}/${encodeURIComponent(target.id)}?force=true`, `Container ${target.name} deleted`, {
			refresh: () => {
				containers = containers.filter((c) => c.id !== target.id);
			}
		});
		actionBusy = null;
		pendingDelete = null;
	}

	// Group containers by owning stack so the table mirrors the Stacks page:
	// stack-owned rows sort under a project header, everything else ("other")
	// follows ungrouped. Stacks sort A→Z for a stable order.
	interface StackGroup {
		name: string;
		items: ContainerInfo[];
	}
	const groups = $derived.by(() => {
		const byStack = new Map<string, ContainerInfo[]>();
		const others: ContainerInfo[] = [];
		for (const c of containers) {
			const s = stackOf(c);
			if (s) {
				const arr = byStack.get(s) ?? [];
				arr.push(c);
				byStack.set(s, arr);
			} else {
				others.push(c);
			}
		}
		const named: StackGroup[] = [...byStack.entries()]
			.sort(([a], [b]) => a.localeCompare(b))
			.map(([name, items]) => ({ name, items }));
		return others.length > 0 ? [...named, { name: '', items: others }] : named;
	});
</script>

<div class="space-y-4">
	<div class="flex gap-1 border-b border-zinc-800" role="tablist">
		{#each tabs as tab}
			<button
				role="tab"
				aria-selected={activeTab === tab}
				onclick={() => { activeTab = tab; }}
				class="px-3 py-2 text-sm transition-colors border-b-2 -mb-px"
				class:border-white={activeTab === tab}
				class:text-white={activeTab === tab}
				class:border-transparent={activeTab !== tab}
				class:text-zinc-500={activeTab !== tab}
				class:hover:text-zinc-300={activeTab !== tab}
			>
				{tab === 'containers' ? 'Containers' : 'Images'}
			</button>
		{/each}
	</div>

	{#if activeTab === 'images'}
		<ImagesPage hideHeader={true} />
	{:else if activeTab === 'containers'}

	{#if error}
		<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm">
			<p class="text-sm text-red-400">{error}</p>
		</div>
	{/if}

	<table class="w-full bg-zinc-900 border border-zinc-800 rounded-sm overflow-hidden">
		<thead>
			<tr class="border-b border-zinc-800">
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Name</th>
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Image</th>
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">State</th>
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Ports</th>
				<th class="right px-4 py-2.5 text-xs font-medium text-zinc-500">Actions</th>
			</tr>
		</thead>
		<tbody>
			{#each groups as group (group.name || '__other__')}
				{#if group.name}
					<tr class="border-b border-zinc-800/50 bg-zinc-800/30">
						<td colspan="5" class="px-4 py-1.5">
							<button
								class="flex items-center gap-2 text-xs font-medium text-sky-400 hover:text-sky-300"
								title="Open this stack"
								onclick={() => openStack(group.name)}
							>
								<Icon name="stacks" />
								Stack: {group.name}
							</button>
						</td>
					</tr>
				{/if}
			{#each group.items as container (container.id)}
				<tr class="border-b border-zinc-800/50 hover:bg-zinc-800/20 transition-colors">
						<td class="px-4 py-2.5 text-sm text-white">
							<button class="hover:text-sky-400 hover:underline" onclick={() => navigate('container-detail', { id: container.id })}>{container.name}</button>
						</td>
					<td class="px-4 py-2.5 text-sm text-zinc-400 font-mono">{container.image}</td>
					<td class="px-4 py-2.5">
						<span class={`px-2 py-0.5 rounded text-xs font-medium ${(container.state === 'running' ? 'bg-emerald-400/10 text-emerald-400' : 'bg-zinc-400/10 text-zinc-400')}`}>
							{container.state}
						</span>
					</td>
					<td class="px-4 py-2.5 text-sm text-zinc-400 font-mono">
						{#if Array.isArray(container.ports) && container.ports.length > 0}
							{@const ports = dedupePorts(container.ports)}
							{#each ports.slice(0, 2) as port}<code class="mr-2 text-xs">{formatPort(port)}</code>{/each}
							{#if ports.length > 2}<span class="text-zinc-600 text-xs">+{ports.length - 2}</span>{/if}
						{:else}
							<span class="text-zinc-600 text-xs">No ports</span>
						{/if}
					</td>
						<td class="px-4 py-2.5">
							<div class="flex items-center gap-1.5">
								{#if $isOperator}
									<Button variant="primary" size="sm" title="Start" aria-label="Start" disabled={actionBusy === container.id || container.state === 'running'} onclick={() => runAction('start', container.id)}>
										<Icon name="play" />
									</Button>
									<Button variant="danger" size="sm" title="Stop" aria-label="Stop" disabled={actionBusy === container.id || container.state !== 'running'} onclick={() => runAction('stop', container.id)}>
										<Icon name="stop" />
									</Button>
									<Button variant="secondary" size="sm" title="Restart" aria-label="Restart" disabled={actionBusy === container.id || container.state !== 'running'} onclick={() => runAction('restart', container.id)}>
										<Icon name="restart" />
									</Button>
									<Button variant="danger" size="sm" title="Delete" aria-label="Delete" disabled={actionBusy === container.id} onclick={() => (pendingDelete = container)}>
										<Icon name="trash" />
									</Button>
								{/if}
							</div>
						</td>
				</tr>
			{/each}
			{/each}
			{#if containers.length === 0}
				<tr><td colspan="5" class="text-center py-8 text-zinc-600">{loading ? 'Loading...' : 'No containers found'}</td></tr>
			{/if}
		</tbody>
	</table>

		<!-- Delete confirmation — stack-owned containers get an explicit warning
		     because deleting one makes its stack go "partial" (see Stacks). -->
		{@const pendingStack = pendingDelete ? stackOf(pendingDelete) : null}
		<ConfirmDialog
			open={pendingDelete !== null}
			title={pendingStack ? 'Delete stack-managed container' : 'Delete container'}
			message={pendingStack
				? `${pendingDelete?.name} is managed by stack "${pendingStack}". Deleting it here leaves the stack "partial" — it comes back on the next stack deploy/restart. Prefer Stacks → ${pendingStack} → Down to stop the whole stack cleanly. Delete anyway?`
				: `Delete container ${pendingDelete?.name ?? ''}? This stops and removes the container. Its volumes are kept unless you remove them separately. This cannot be undone.`}
			busy={actionBusy === pendingDelete?.id}
			onconfirm={confirmDelete}
			onclose={() => (pendingDelete = null)}
		/>
	{/if}
</div>
