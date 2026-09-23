<script lang="ts">
  // Top-level networks editor — Svelte port of Dockge's NetworkInput.vue.
  // Simplified to explicit binding: edits mutate the bound `networks` object
  // (jsonConfig.networks), which the parent regenerates YAML from.
  interface Props {
    networks?: Record<string, any>;
    externalOptions?: string[]; // docker networks present on the host
  }

  let {
    networks = $bindable<Record<string, any> | undefined>(undefined),
    externalOptions = []
  }: Props = $props();

  let newName = $state('');
  let selectedExternal = $state('');

  const internalNames = $derived(
    Object.keys(networks ?? {}).filter((n) => !networks?.[n]?.external)
  );
  const externalNames = $derived(
    Object.keys(networks ?? {}).filter((n) => networks?.[n]?.external)
  );

  function ensure(): Record<string, any> {
    if (!networks || typeof networks !== 'object') networks = {};
    return networks;
  }

  function addInternal() {
    const n = newName.trim();
    if (!n) return;
    const next = { ...ensure() };
    if (!(n in next)) next[n] = {};
    networks = next;
    newName = '';
  }

  function addExternal() {
    if (!selectedExternal) return;
    const next = { ...ensure() };
    next[selectedExternal] = { external: true };
    networks = next;
    selectedExternal = '';
  }

  function remove(name: string) {
    if (!networks) return;
    const next = { ...networks };
    delete next[name];
    networks = next;
  }
</script>

<div class="space-y-3">
  {#if internalNames.length === 0 && externalNames.length === 0}
    <p class="text-sm text-zinc-500">No custom networks — services use the stack's default network.</p>
  {/if}

  {#each internalNames as name (name)}
    <div class="flex items-center justify-between rounded-sm bg-zinc-900 px-2 py-1">
      <span class="text-sm text-zinc-200">{name}</span>
      <button class="text-xs text-zinc-400 hover:text-red-400" onclick={() => remove(name)}>remove</button>
    </div>
  {/each}
  {#each externalNames as name (name)}
    <div class="flex items-center justify-between rounded-sm bg-zinc-900 px-2 py-1">
      <span class="text-sm text-zinc-200">{name} <span class="text-xs text-amber-400">(external)</span></span>
      <button class="text-xs text-zinc-400 hover:text-red-400" onclick={() => remove(name)}>remove</button>
    </div>
  {/each}

  <div class="flex gap-2">
    <input
      class="flex-1 rounded-sm border border-zinc-700 bg-zinc-900 px-2 py-1 text-sm text-zinc-100 focus:border-zinc-500 focus:outline-none"
      placeholder="New network name…"
      bind:value={newName}
      onkeydown={(e) => e.key === 'Enter' && addInternal()}
    />
    <button
      class="rounded-sm bg-zinc-700 px-3 py-1 text-sm text-zinc-100 hover:bg-zinc-600"
      onclick={addInternal}
    >
      + Network
    </button>
  </div>

  {#if externalOptions.length > 0}
    <div class="flex gap-2">
      <select
        class="flex-1 rounded-sm border border-zinc-700 bg-zinc-900 px-2 py-1 text-sm text-zinc-100 focus:border-zinc-500 focus:outline-none"
        bind:value={selectedExternal}
      >
        <option value="">Attach existing docker network…</option>
        {#each externalOptions as opt}
          <option value={opt}>{opt}</option>
        {/each}
      </select>
      <button
        class="rounded-sm bg-zinc-700 px-3 py-1 text-sm text-zinc-100 hover:bg-zinc-600"
        onclick={addExternal}
      >
        + External
      </button>
    </div>
  {/if}
</div>
