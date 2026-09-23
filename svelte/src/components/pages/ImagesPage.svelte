<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api/client';
	import { addToast } from '$lib/store';
	import type { ImageInfo } from '$lib/types/generated';
	import Button from '../ui/Button.svelte';

	let images = $state<ImageInfo[]>([]);
	let loading = $state(true);
	let error = $state('');
	let pullTarget = $state('');
	let busy = $state(false);
	let pruneBusy = $state(false);

	onMount(load);

	async function load() {
		loading = true;
		error = '';
		try {
			images = await api.get<ImageInfo[]>('/images');
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load images';
		} finally {
			loading = false;
		}
	}

	async function pullImage() {
		if (!pullTarget.trim()) return;
		busy = true;
		try {
			await api.post('/images/pull', { image: pullTarget.trim() });
			addToast(`Pulled ${pullTarget.trim()}`, 'success');
			pullTarget = '';
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Pull failed', 'error');
		} finally {
			busy = false;
		}
	}

	async function removeImage(id: string) {
		try {
			await api.delete(`/images/${id}`);
			addToast('Image removed', 'success');
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Remove failed', 'error');
		}
	}

	async function forcePull(image: ImageInfo) {
		try {
			await api.post('/images/pull-force', { image: `${image.repo}:${image.tag}` });
			addToast(`Force pulled ${image.repo}:${image.tag}`, 'success');
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Force pull failed', 'error');
		}
	}

	async function checkImageUpdate(image: ImageInfo) {
		try {
			const result = await api.post<{ has_update: boolean }>(`/images/check`, {
				image: `${image.repo}:${image.tag}`
			});
			addToast(
				result.has_update ? `Update available for ${image.repo}:${image.tag}` : `${image.repo}:${image.tag} is up to date`,
				result.has_update ? 'info' : 'success'
			);
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Update check failed', 'error');
		}
	}

	async function pruneImages() {
		pruneBusy = true;
		try {
			const result = await api.post<{ images_deleted: number; space_reclaimed: number }>('/images/prune', { dangling_only: true });
			addToast(`Pruned ${result.images_deleted ?? 0} dangling image(s), reclaimed ${formatBytes(result.space_reclaimed ?? 0)}`, 'success');
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Prune failed', 'error');
		} finally {
			pruneBusy = false;
		}
	}

	function formatBytes(bytes: number): string {
		if (bytes === 0) return '0 B';
		const units = ['B', 'KB', 'MB', 'GB'];
		const idx = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
		return `${(bytes / Math.pow(1024, idx)).toFixed(1)} ${units[idx]}`;
	}

	async function checkUpdates() {
		try {
			images = await api.get<ImageInfo[]>('/images');
			addToast('Update check refreshed', 'info');
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Update check failed', 'error');
		}
	}
</script>

<div class="space-y-4">
	<div class="flex items-center justify-between">
		<h2 class="text-lg font-semibold text-white">Images</h2>
		<div class="flex gap-2">
			<Button variant="secondary" size="sm" onclick={checkUpdates}>Refresh</Button>
			<Button variant="secondary" size="sm" loading={pruneBusy} onclick={pruneImages}>Prune dangling</Button>
		</div>
	</div>

	<form class="flex gap-2" onsubmit={(e) => { e.preventDefault(); pullImage(); }}>
		<input
			type="text"
			bind:value={pullTarget}
			placeholder="e.g. nginx:latest"
			class="flex-1 px-3 py-2 bg-zinc-900 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
		/>
		<Button type="submit" loading={busy}>Pull image</Button>
	</form>

	{#if error}
		<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm">
			<p class="text-sm text-red-400">{error}</p>
		</div>
	{/if}

	<div class="bg-zinc-900 border border-zinc-800 rounded-sm overflow-hidden">
		<table class="w-full">
			<thead>
				<tr class="border-b border-zinc-800">
					<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Repository</th>
					<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Tag</th>
					<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Size</th>
					<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Created</th>
					<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Update</th>
					<th class="text-right px-4 py-2.5 text-xs font-medium text-zinc-500">Actions</th>
				</tr>
			</thead>
			<tbody>
				{#each images as image (image.id)}
					<tr class="border-b border-zinc-800/50 hover:bg-zinc-800/20 transition-colors">
						<td class="px-4 py-2.5 text-sm text-white font-mono">{image.repo}</td>
						<td class="px-4 py-2.5 text-sm text-zinc-400">{image.tag}</td>
						<td class="px-4 py-2.5 text-sm text-zinc-400">{image.size}</td>
						<td class="px-4 py-2.5 text-sm text-zinc-500">{image.created}</td>
						<td class="px-4 py-2.5">
							{#if image.has_update}
								<span class="px-2 py-0.5 rounded text-xs font-medium bg-amber-400/10 text-amber-400">update available</span>
							{:else}
								<span class="text-xs text-zinc-600">—</span>
							{/if}
						</td>
						<td class="px-4 py-2.5 text-right space-x-2">
							<Button variant="secondary" size="sm" onclick={() => checkImageUpdate(image)}>Check update</Button>
							{#if image.has_update}
								<Button variant="primary" size="sm" onclick={() => forcePull(image)}>Force pull</Button>
							{/if}
							<Button variant="danger" size="sm" onclick={() => removeImage(image.id)}>Delete</Button>
						</td>
					</tr>
				{:else}
					<tr>
						<td colspan="6" class="text-center py-8 text-zinc-600 text-sm">
							{loading ? 'Loading images...' : 'No images'}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
</div>
