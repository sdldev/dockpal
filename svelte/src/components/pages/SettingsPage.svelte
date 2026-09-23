<script lang="ts">
	import { onMount } from 'svelte';
	import { api, clearToken } from '$lib/api/client';
	import { currentUser, addToast } from '$lib/store';
	import { navigate } from '$lib/router';
	import Button from '../ui/Button.svelte';
	import Modal from '../ui/Modal.svelte';

	let user = $state<{ username: string; role: string; created_at: number } | null>(null);
	let loading = $state(true);
	let error = $state('');
	// Change password state
	let showChangePassword = $state(false);
	let currentPassword = $state('');
	let newPassword = $state('');
	let confirmNewPassword = $state('');
	let savingPassword = $state(false);
	let passError = $state('');
	// Quick reset state (POST /api/auth/reset-password — no current password needed)
	let showQuickReset = $state(false);
	let quickResetPassword = $state('');
	let quickResetting = $state(false);

	onMount(async () => {
		loading = true;
		error = '';
		try {
			user = await api.get('/profile');
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to load profile';
		} finally {
			loading = false;
		}
	});

	async function savePassword() {
		if (!currentPassword || !newPassword || !confirmNewPassword) {
			passError = 'All fields required';
			return;
		}
		if (newPassword !== confirmNewPassword) {
			passError = 'Passwords do not match';
			return;
		}
		if (newPassword.length < 8) {
			passError = 'New password must be at least 8 characters';
			return;
		}
		savingPassword = true;
		passError = '';
		try {
			await api.put('/profile/password', {
				current_password: currentPassword,
				new_password: newPassword
			});
			addToast('Password updated', 'success');
			showChangePassword = false;
			currentPassword = '';
			newPassword = '';
			confirmNewPassword = '';
			// Refresh user data if needed
		} catch (e: unknown) {
			const msg = e instanceof Error ? e.message : 'Failed to update password';
			// Common backend errors
			if (msg.includes('current password')) passError = 'Current password is incorrect';
			else passError = msg;
			addToast(msg, 'error');
		} finally {
			savingPassword = false;
		}
	}

	function logout() {
		api.post('/logout').catch(() => {});
		clearToken();
		currentUser.set(null);
		addToast('Logged out', 'info');
		navigate('dashboard', {}, true);
	}

	async function quickReset() {
		if (quickResetPassword.length < 8) {
			addToast('Password must be at least 8 characters', 'error');
			return;
		}
		quickResetting = true;
		try {
			await api.post('/auth/reset-password', { new_password: quickResetPassword });
			addToast('Password reset — please log in again', 'success');
			showQuickReset = false;
			quickResetPassword = '';
			logout();
		} catch (e) {
			addToast(e instanceof Error ? e.message : 'Reset failed', 'error');
		} finally {
			quickResetting = false;
		}
	}

	function formatCreatedAt(ts: number) {
		// Backend returns Unix seconds
		return new Date(ts * 1000).toLocaleString();
	}
</script>

<div class="space-y-6">
	<div>
		<h2 class="text-lg font-semibold text-white">Profile</h2>
		<p class="text-sm text-zinc-500">Manage your account and security settings</p>
	</div>

	{#if error && !user}
		<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm">
			<p class="text-sm text-red-400">{error}</p>
		</div>
	{/if}

	{#if !loading && user}
		<div class="bg-zinc-900 border border-zinc-800 rounded-sm p-4 space-y-3">
			<div class="grid grid-cols-2 gap-4">
				<div>
					<div class="text-xs text-zinc-500 mb-1">Username</div>
					<div class="text-sm text-white">{user.username}</div>
				</div>
				<div>
					<div class="text-xs text-zinc-500 mb-1">Role</div>
					<div class="text-sm">
						<span class="px-2 py-0.5 rounded text-xs font-medium bg-blue-400/10 text-blue-400 capitalize">
							{user.role}
						</span>
					</div>
				</div>
			</div>
			<div>
				<div class="text-xs text-zinc-500 mb-1">Created at</div>
				<div class="text-sm text-zinc-400">{formatCreatedAt(user.created_at)}</div>
			</div>
			<div class="pt-3 border-t border-zinc-800 flex flex-wrap gap-3">
				<Button variant="secondary" onclick={() => showChangePassword = true}>Change Password</Button>
				<Button variant="secondary" onclick={() => { showQuickReset = true; }}>Quick Reset</Button>
				<Button variant="danger" onclick={logout}>Logout</Button>
			</div>
		</div>
	{:else}
		<div class="text-zinc-500">Loading profile...</div>
	{/if}

	<Modal open={showChangePassword} title="Change Password" size="sm" onclose={() => showChangePassword = false}>
		<div class="space-y-4">
			{#if passError}
				<div class="p-3 bg-red-500/10 border border-red-500/20 rounded-sm text-sm text-red-400">{passError}</div>
			{/if}
			<div>
				<label for="cpwd" class="block text-xs font-medium text-zinc-400 mb-1">Current password</label>
				<input
					id="cpwd"
					type="password"
					bind:value={currentPassword}
					class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
				/>
			</div>
			<div>
				<label for="npwd" class="block text-xs font-medium text-zinc-400 mb-1">New password</label>
				<input
					id="npwd"
					type="password"
					bind:value={newPassword}
					class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
				/>
			</div>
			<div>
				<label for="cnpwd" class="block text-xs font-medium text-zinc-400 mb-1">Confirm new password</label>
				<input
					id="cnpwd"
					type="password"
					bind:value={confirmNewPassword}
					class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
				/>
			</div>
			<div class="flex justify-end gap-2 pt-2">
				<Button variant="secondary" disabled={savingPassword} onclick={() => showChangePassword = false}>Cancel</Button>
				<Button variant="primary" loading={savingPassword} onclick={savePassword}>Save changes</Button>
			</div>
		</div>
	</Modal>

	<Modal open={showQuickReset} title="Quick password reset" size="sm" onclose={() => showQuickReset = false}>
		<div class="space-y-4">
			<p class="text-xs text-zinc-500">
				Sets a new password directly (no current password required).
				You will be logged out afterwards.
			</p>
			<div>
				<label for="qrpwd" class="block text-xs font-medium text-zinc-400 mb-1">New password</label>
				<input
					id="qrpwd"
					type="password"
					bind:value={quickResetPassword}
					class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
				/>
			</div>
			<div class="flex justify-end gap-2 pt-2">
				<Button variant="secondary" disabled={quickResetting} onclick={() => showQuickReset = false}>Cancel</Button>
				<Button variant="danger" loading={quickResetting} onclick={quickReset}>Reset password</Button>
			</div>
		</div>
	</Modal>
</div>
