<script lang="ts">
  // Dynamic row editor backed by a fixed option list (e.g. networks).
  // Svelte port of Dockge's ArraySelect.vue with explicit bindable props.
  interface Props {
    values?: string[];
    options: string[];
    placeholder?: string;
    addLabel?: string;
  }

  let {
    values = $bindable<string[]>([]),
    options,
    placeholder = 'Select…',
    addLabel = 'Add'
  }: Props = $props();

  function addField() {
    values = [...values, options[0] ?? ''];
  }

  function remove(index: number) {
    values = values.filter((_, i) => i !== index);
  }

  function update(index: number, v: string) {
    const next = values.slice();
    next[index] = v;
    values = next;
  }
</script>

<div class="space-y-2">
  {#each values as value, i (i)}
    <div class="flex gap-2">
      <select
        class="flex-1 rounded-sm border border-zinc-700 bg-zinc-900 px-2 py-1 text-sm text-zinc-100 focus:border-zinc-500 focus:outline-none"
        value={value}
        onchange={(e) => update(i, (e.target as HTMLSelectElement).value)}
      >
        <option value="" disabled>{placeholder}</option>
        {#each options as opt}
          <option value={opt}>{opt}</option>
        {/each}
        {#if value && !options.includes(value)}
          <option value={value}>{value} (not found)</option>
        {/if}
      </select>
      <button
        type="button"
        class="rounded-sm bg-zinc-700 px-2 text-sm text-zinc-200 hover:bg-zinc-600"
        title="Remove"
        onclick={() => remove(i)}
      >
        ✕
      </button>
    </div>
  {/each}
  <button
    type="button"
    class="rounded-sm border border-dashed border-zinc-600 px-3 py-1 text-sm text-zinc-400 hover:border-zinc-400 hover:text-zinc-200"
    onclick={addField}
  >
    + {addLabel}
  </button>
</div>
