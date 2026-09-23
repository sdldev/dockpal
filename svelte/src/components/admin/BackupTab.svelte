<script lang="ts">
	import { api } from '$lib/api/client';
	import { addToast } from '$lib/store';
	import type { BackupResult } from '$lib/types/generated';
	import Button from '../ui/Button.svelte';

	let busy = $state(false);
	let result = $state<BackupResult | null>(null);
	let error = $state('');

	async function triggerBackup() {
		busy = true;
		error = '';
		result = null;
		try {
			result = await api.post<BackupResult>('/backup');
			addToast('Backup completed', 'success');
		} catch (e) {
			error = e instanceof Error ? e.message : 'Backup failed';
			addToast(error, 'error');
		} finally {
			busy = false;
		}
	}

	function formatSize(bytes: number) {
		if (!bytes) return '0 B';
		const units = ['B', 'KB', 'MB', 'GB'];
		const idx = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
		return `${(bytes / Math.pow(1024, idx)).toFixed(1)} ${units[idx]}`;
	}
</script>

<div class="space-y-4">
	<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4">
		<h3 class="text-sm font-medium text-white mb-1">On-demand backup</h3>
		<p class="text-xs text-zinc-500 mb-4">
			Creates a BBolt snapshot + SHA-256 checksum in the server backup directory.
			Scheduled backups run per DOCKPAL_BACKUP_INTERVAL.
		</p>
		<Button variant="primary" loading={busy} onclick={triggerBackup}>
			{busy ? 'Backing up...' : 'Run backup now'}
		</Button>
	</div>

	{#if error}
		<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm">
			<p class="text-sm text-red-400">{error}</p>
		</div>
	{/if}

	{#if result}
		<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4 space-y-2">
			<h4 class="text-sm font-medium text-emerald-400">✓ Backup complete</h4>
			<dl class="text-xs space-y-1">
				<div class="flex gap-2">
					<dt class="text-zinc-500 w-32">Path</dt>
					<dd class="text-white font-mono break-all">{result.path}</dd>
				</div>
				<div class="flex gap-2">
					<dt class="text-zinc-500 w-32">Size</dt>
					<dd class="text-zinc-400">{formatSize(result.size)}</dd>
				</div>
				<div class="flex gap-2">
					<dt class="text-zinc-500 w-32">Checksum</dt>
					<dd class="text-zinc-400 font-mono break-all">{result.checksum || '—'}</dd>
				</div>
				<div class="flex gap-2">
					<dt class="text-zinc-500 w-32">Verified</dt>
					<dd class="text-zinc-400">{result.checksum_verified ? 'yes' : 'no'}</dd>
				</div>
				<div class="flex gap-2">
					<dt class="text-zinc-500 w-32">Timestamp</dt>
					<dd class="text-zinc-400">{result.timestamp}</dd>
				</div>
			</dl>
		</div>
	{/if}
</div>
