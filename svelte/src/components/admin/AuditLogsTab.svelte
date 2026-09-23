<script lang="ts">
	import { api } from '$lib/api/client';
	import type { AuditLogResponse } from '$lib/types/generated';
	import Button from '../ui/Button.svelte';

	let logs = $state<AuditLogResponse | null>(null);
	let loading = $state(true);
	let error = $state('');
	let offset = $state(0);
	const limit = 50;

	async function load() {
		loading = true;
		error = '';
		try {
			logs = await api.get<AuditLogResponse>(`/audit-logs?limit=${limit}&offset=${offset}`);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load audit logs';
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		load();
	});

	function nextPage() {
		if (logs && offset + limit < logs.total) {
			offset += limit;
		}
	}

	function prevPage() {
		if (offset >= limit) offset -= limit;
	}

	function formatTime(ts: number) {
		return new Date(ts * 1000).toLocaleString('en-US', { hour12: false });
	}

	function statusClass(status: string) {
		return status === 'success'
			? 'bg-emerald-400/10 text-emerald-400'
			: 'bg-red-400/10 text-red-400';
	}
</script>

{#if error}
	<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm">
		<p class="text-sm text-red-400">{error}</p>
	</div>
{/if}

<div class="flex items-center justify-between mb-2">
	<span class="text-xs text-zinc-500">
		{logs ? `${logs.total} entries — showing ${offset + 1}–${Math.min(offset + limit, logs.total)}` : ''}
	</span>
	<div class="flex gap-2">
		<Button variant="secondary" size="sm" disabled={offset === 0} onclick={prevPage}>← Prev</Button>
		<Button variant="secondary" size="sm" disabled={!logs || offset + limit >= logs.total} onclick={nextPage}>Next →</Button>
	</div>
</div>

<div class="bg-zinc-900 border border-zinc-800 rounded-sm overflow-hidden">
	<table class="w-full">
		<thead>
			<tr class="border-b border-zinc-800">
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Time</th>
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">User</th>
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Action</th>
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Resource</th>
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Status</th>
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">IP</th>
			</tr>
		</thead>
		<tbody>
			{#each logs?.logs ?? [] as log (log.id)}
				<tr class="border-b border-zinc-800/50 hover:bg-zinc-800/20 transition-colors">
					<td class="px-4 py-2.5 text-xs text-zinc-500 whitespace-nowrap">{formatTime(log.timestamp)}</td>
					<td class="px-4 py-2.5 text-sm text-white">{log.username}</td>
					<td class="px-4 py-2.5 text-sm text-zinc-400">{log.action}</td>
					<td class="px-4 py-2.5 text-sm text-zinc-400 font-mono">{log.resource}</td>
					<td class="px-4 py-2.5">
						<span class="px-2 py-0.5 rounded text-xs font-medium {statusClass(log.status)}">{log.status}</span>
					</td>
					<td class="px-4 py-2.5 text-xs text-zinc-600 font-mono">{log.ip_address}</td>
				</tr>
			{:else}
				<tr>
					<td colspan="6" class="text-center py-8 text-zinc-600 text-sm">
						{loading ? 'Loading audit logs...' : 'No audit logs'}
					</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>
