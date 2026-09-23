<script lang="ts">
  // Stack list — Dockge-style overview of compose projects with status pills.
  // Instance-aware: the picker targets local or a remote agent instance.
  import { onMount } from 'svelte';
  import { listStacks, listInstances, type Stack, type InstanceListItem } from '$lib/api/stacks';
  import { stackStatusColor } from '$lib/stack-utils';
  import { currentStackName, selectedInstance } from '$lib/store';
  import { navigate } from '$lib/router';

  let stacks = $state<Stack[]>([]);
  let instances = $state<InstanceListItem[]>([]);
  let loading = $state(true);
  let error = $state('');

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

  async function loadInstances() {
    try {
      const res = await listInstances();
      const list = Array.isArray(res) ? res : (res.instances ?? []);
      // "local" is rendered as the hardcoded first option — drop the API's
      // pseudo-entry to avoid a duplicate.
      instances = list.filter((i) => i.id !== 'local');

      // Fallback: if the stored selection no longer exists (e.g. fresh data
      // dir or removed instance), reset to local and refresh.
      const selected = $selectedInstance;
      const exists = selected === 'local' || list.some((i) => i.id === selected);
      if (!exists) {
        selectedInstance.set('local');
        localStorage.setItem('dockpal_selected_instance', 'local');
        await refresh();
      }
    } catch {
      instances = [];
    }
  }

  function onInstanceChange(e: Event) {
    const id = (e.target as HTMLSelectElement).value;
    selectedInstance.set(id);
    localStorage.setItem('dockpal_selected_instance', id);
    refresh();
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
    loadInstances();
    refresh();
  });
</script>

<div class="max-w-5xl">
  <div class="mb-6 flex items-center justify-between">
    <div>
      <h1 class="text-2xl font-bold text-white">Compose Stacks</h1>
      <p class="mt-1 text-sm text-zinc-400">Dockge-style compose.yaml + .env stacks</p>
    </div>
    <div class="flex items-center gap-3">
      <label class="flex items-center gap-2 text-sm text-zinc-400">
        Instance
        <select
          class="rounded-sm border border-zinc-700 bg-zinc-900 px-2 py-1.5 text-sm text-zinc-100 focus:border-zinc-500 focus:outline-none"
          value={$selectedInstance}
          onchange={onInstanceChange}
        >
          <option value="local">local (this host)</option>
          {#each instances as inst (inst.id)}
            <option value={inst.id} disabled={inst.status === 'offline'}>
              {inst.name} ({inst.status})
            </option>
          {/each}
        </select>
      </label>
      <button
        class="rounded-sm bg-emerald-600 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-500"
        onclick={createStack}
      >
        + Create Stack
      </button>
    </div>
  </div>

  {#if loading}
    <div class="text-zinc-500">Loading…</div>
  {:else if error}
    <div class="rounded-md border border-red-800 bg-red-900/30 p-4 text-sm text-red-300">{error}</div>
  {:else if stacks.length === 0}
    <div class="rounded-md border border-zinc-800 bg-zinc-900 p-8 text-center text-zinc-500">
      No stacks on this instance yet. Create one to get started.
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
</div>