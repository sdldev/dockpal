<script lang="ts">
  // Stack list — Dockge-style overview of compose projects with status pills.
  // Instance-aware: the picker targets local or a remote agent instance.
  // The Catalog tab hosts the former "App Installer" (one-click templates +
  // raw Compose/Git deploys) so "create something" has one home.
  import { onMount } from 'svelte';
  import { listStacks, listInstances, type Stack } from '$lib/api/stacks';
  import { stackStatusColor } from '$lib/stack-utils';
  import { currentStackName, selectedInstance } from '$lib/store';
  import { navigate } from '$lib/router';
  import CatalogTab from '../stacks/CatalogTab.svelte';
  import AppsPage from './AppsPage.svelte';

  let stacks = $state<Stack[]>([]);
  let loading = $state(true);
  let error = $state('');

  const tabs = ['stacks', 'catalog', 'updates'] as const;
  type Tab = (typeof tabs)[number];
  let activeTab = $state<Tab>('stacks');

  async function refresh() {
    loading = true;
    error = '';
    try {
      const res = await listStacks($selectedInstance);
      stacks = res.stacks ?? [];
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to load stacks';
      stacks = [];
    } finally {
      loading = false;
    }
  }

  // Fallback only: if the stored selection no longer exists (fresh data dir
  // or removed instance), reset to local. Picking an instance is the
  // NavHeader server switcher's job — this page just follows the store.
  async function ensureSelectionValid() {
    try {
      const res = await listInstances();
      const list = Array.isArray(res) ? res : (res.instances ?? []);
      const selected = $selectedInstance;
      const exists = selected === 'local' || list.some((i) => i.id === selected);
      if (!exists) {
        selectedInstance.set('local');
        localStorage.setItem('dockpal_selected_instance', 'local');
      }
    } catch { /* keep current selection on error */ }
  }

  function openStack(name: string) {
    currentStackName.set(name);
    navigate('compose');
  }

  function createStack() {
    currentStackName.set(null);
    navigate('compose');
  }

  onMount(() => {
    ensureSelectionValid();
  });

  // Reload whenever the NavHeader switcher changes the target instance
  // (covers mount and the fallback reset above too).
  $effect(() => {
    void $selectedInstance;
    refresh();
  });
</script>

<div class="max-w-6xl">
  <div class="mb-6 flex items-center justify-end gap-3">
    <button
      class="rounded-sm bg-emerald-600 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-500"
      onclick={createStack}
    >
      + Create Stack
    </button>
  </div>

  <div class="mb-4 flex gap-1 border-b border-zinc-800">
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
        {tab === 'stacks' ? 'My Stacks' : tab === 'catalog' ? 'Catalog' : 'Updates'}
      </button>
    {/each}
  </div>

  {#if activeTab === 'stacks'}
    {#if loading}
      <div class="text-zinc-500">Loading…</div>
    {:else if error}
      <div class="rounded-md border border-red-800 bg-red-900/30 p-4 text-sm text-red-300">{error}</div>
    {:else if stacks.length === 0}
      <div class="rounded-md border border-zinc-800 bg-zinc-900 p-8 text-center text-zinc-500">
        No stacks on this instance yet. <button class="text-emerald-400 hover:underline" onclick={createStack}>Create one</button> or browse the Catalog.
      </div>
    {:else}
      <div class="space-y-2">
        {#each stacks as stack (stack.name)}
          <button
            class="flex w-full items-center justify-between rounded-md border border-zinc-800 bg-zinc-900 px-4 py-3 text-left transition-colors hover:border-zinc-600"
            onclick={() => openStack(stack.name)}
          >
            <div class="flex items-center gap-3">
              <span class="rounded px-2 py-0.5 text-xs font-medium {stackStatusColor(stack.status)}">
                {stack.status}
              </span>
              <span class="font-medium text-white">{stack.name}</span>
              {#if !stack.managed}
                <span class="text-xs text-zinc-500">(external)</span>
              {/if}
            </div>
            <span class="text-sm text-zinc-500">{stack.statusText}</span>
          </button>
        {/each}
      </div>
    {/if}
  {:else if activeTab === 'catalog'}
    <CatalogTab />
  {:else if activeTab === 'updates'}
    <AppsPage hideHeader={true} />
  {/if}
</div>
