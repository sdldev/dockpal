<script lang="ts">
	import { api } from '$lib/api/client';
	import { currentUser, addToast } from '$lib/store';
	import type { AdminUser } from '$lib/types/generated';
	import Button from '../ui/Button.svelte';

	let users = $state<AdminUser[]>([]);
	let loading = $state(true);
	let error = $state('');
	let busy = $state<string | null>(null);

	const roles: Array<'admin' | 'operator' | 'viewer'> = ['admin', 'operator', 'viewer'];

	async function load() {
		loading = true;
		error = '';
		try {
			users = await api.get<AdminUser[]>('/users');
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load users';
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		load();
	});

	async function changeRole(username: string, role: string) {
		if (username === $currentUser?.username) {
			addToast('You cannot change your own role', 'error');
			return;
		}
		busy = username;
		try {
			await api.put(`/users/${username}/role`, { role });
			addToast(`Role of ${username} set to ${role}`, 'success');
			await load();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Role update failed', 'error');
		} finally {
			busy = null;
		}
	}
</script>

{#if error}
	<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm">
		<p class="text-sm text-red-400">{error}</p>
	</div>
{/if}

<div class="bg-zinc-900 border border-zinc-800 rounded-sm overflow-hidden">
	<table class="w-full">
		<thead>
			<tr class="border-b border-zinc-800">
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Username</th>
				<th class="text-left px-4 py-2.5 text-xs font-medium text-zinc-500">Role</th>
				<th class="text-right px-4 py-2.5 text-xs font-medium text-zinc-500">Change role</th>
			</tr>
		</thead>
		<tbody>
			{#each users as user (user.username)}
				<tr class="border-b border-zinc-800/50 hover:bg-zinc-800/20 transition-colors">
					<td class="px-4 py-2.5 text-sm text-white">
						{user.username}
						{#if user.username === $currentUser?.username}
							<span class="text-xs text-zinc-500">(you)</span>
						{/if}
					</td>
					<td class="px-4 py-2.5">
						<span class="px-2 py-0.5 rounded text-xs font-medium bg-blue-400/10 text-blue-400 capitalize">
							{user.role}
						</span>
					</td>
					<td class="px-4 py-2.5 text-right space-x-2">
						{#each roles as role}
							{#if role !== user.role}
								<Button
									variant="secondary"
									size="sm"
									disabled={busy === user.username || user.username === $currentUser?.username}
									onclick={() => changeRole(user.username, role)}
								>
									→ {role}
								</Button>
							{/if}
						{/each}
					</td>
				</tr>
			{:else}
				<tr>
					<td colspan="3" class="text-center py-8 text-zinc-600 text-sm">
						{loading ? 'Loading users...' : 'No users'}
					</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>
