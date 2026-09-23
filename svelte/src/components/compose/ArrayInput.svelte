<script lang="ts">
  // Dynamic row editor for string arrays (ports, volumes, environment, ...).
  // Svelte port of Dockge's ArrayInput.vue — but with explicit bindable props
  // instead of $parent tunneling.
  interface Props {
    values?: string[];
    placeholder?: string;
    addLabel?: string;
    onchange?: (values: string[]) => void;
  }

  let {
    values = $bindable<string[]>([]),
    placeholder = '',
    addLabel = 'Add',
    onchange
  }: Props = $props();

  function emit(next: string[]) {
    values = next;
    onchange?.(next);
  }

  function addField() {
    emit([...values, '']);
  }

  function remove(index: number) {
    emit(values.filter((_, i) => i !== index));
  }

  function update(index: number, v: string) {
    const next = values.slice();
    next[index] = v;
    emit(next);
  }
</script>

<div class="space-y-2">
  {#each values as value, i (i)}
    <div class="flex gap-2">
      <input
        class="flex-1 rounded-sm border border-zinc-700 bg-zinc-900 px-2 py-1 text-sm text-zinc-100 focus:border-zinc-500 focus:outline-none"
        {placeholder}
        value={value}
        oninput={(e) => update(i, (e.target as HTMLInputElement).value)}
      />
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
