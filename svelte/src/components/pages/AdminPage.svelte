<script lang="ts">
	import UsersTab from '../admin/UsersTab.svelte';
	import ApiKeysTab from '../admin/ApiKeysTab.svelte';
	import AuditLogsTab from '../admin/AuditLogsTab.svelte';
	import RegistriesTab from '../admin/RegistriesTab.svelte';
	import BackupTab from '../admin/BackupTab.svelte';
	import SystemInfoTab from '../admin/SystemInfoTab.svelte';
	import TunnelTab from '../admin/TunnelTab.svelte';

	const tabs = ['users', 'api-keys', 'audit-logs', 'registries', 'backup', 'system', 'tunnel'] as const;
	type Tab = (typeof tabs)[number];

	let activeTab = $state<Tab>('users');

	const tabLabels: Record<Tab, string> = {
		users: 'Users',
		'api-keys': 'API Keys',
		'audit-logs': 'Audit Logs',
		registries: 'Registries',
		backup: 'Backup',
		system: 'System',
		tunnel: 'Tunnel'
	};
</script>

<div class="space-y-4">
	<div>
		<h2 class="text-lg font-semibold text-white">Admin</h2>
		<p class="text-sm text-zinc-500">Users, API keys, audit logs, registries, backup</p>
	</div>

	<div class="flex gap-1 border-b border-zinc-800">
		{#each tabs as tab}
			<button
				onclick={() => { activeTab = tab; }}
				class="px-3 py-2 text-sm transition-colors border-b-2 -mb-px"
				class:border-white={activeTab === tab}
				class:text-white={activeTab === tab}
				class:border-transparent={activeTab !== tab}
				class:text-zinc-500={activeTab !== tab}
				class:hover:text-zinc-300={activeTab !== tab}
			>
				{tabLabels[tab]}
			</button>
		{/each}
	</div>

	{#if activeTab === 'users'}
		<UsersTab />
	{:else if activeTab === 'api-keys'}
		<ApiKeysTab />
	{:else if activeTab === 'audit-logs'}
		<AuditLogsTab />
	{:else if activeTab === 'registries'}
		<RegistriesTab />
	{:else if activeTab === 'backup'}
		<BackupTab />
	{:else if activeTab === 'system'}
		<SystemInfoTab />
	{:else if activeTab === 'tunnel'}
		<TunnelTab />
	{/if}
</div>
