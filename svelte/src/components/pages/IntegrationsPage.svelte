<script lang="ts">
	// Integrations — Webhooks (Git deploy triggers) and Domains (reverse-proxy
	// routing) grouped into one page, matching the sidebar IA.
	import WebhooksPage from './WebhooksPage.svelte';
	import DomainsPage from './DomainsPage.svelte';

	const tabs = ['webhooks', 'domains'] as const;
	type Tab = (typeof tabs)[number];
	let activeTab = $state<Tab>('webhooks');
</script>

<div class="space-y-4">
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
				{tab === 'webhooks' ? 'Webhooks' : 'Domains'}
			</button>
		{/each}
	</div>

	{#if activeTab === 'webhooks'}
		<WebhooksPage hideHeader={true} />
	{:else if activeTab === 'domains'}
		<DomainsPage hideHeader={true} />
	{/if}
</div>
