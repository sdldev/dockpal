<script lang="ts">
  // Global compose ENV for the instance — shared by every stack on it.
  // Self-contained collapsible editor; the parent only passes `processing`
  // so saving is disabled while a deploy runs.
  import { onMount } from 'svelte';
  import { get } from 'svelte/store';
  import { getGlobalEnv, setGlobalEnv } from '$lib/api/stacks';
  import { addToast, selectedInstance } from '$lib/store';

  type EditorModule = typeof import('./CodeMirrorEditor.svelte');
  let CodeMirrorEditor = $state<EditorModule['default'] | undefined>(undefined);

  const instanceId = get(selectedInstance) || 'local';

  let globalEnv = $state('');
  let showGlobalEnv = $state(false);
  let globalEnvDirty = $state(false);

  let { processing = false }: { processing?: boolean } = $props();

  async function loadGlobalEnv() {
    try {
      const res = await getGlobalEnv(instanceId);
      globalEnv = res.content ?? '';
      globalEnvDirty = false;
    } catch {
      // No global env configured yet — start the editor empty.
      globalEnv = '';
    }
  }

  async function saveGlobalEnv() {
    try {
      await setGlobalEnv(globalEnv, instanceId);
      globalEnvDirty = false;
      addToast('Global env saved', 'success');
    } catch (e) {
      addToast(e instanceof Error ? e.message : 'Failed to save global env', 'error');
    }
  }

  onMount(() => {
    // Fire-and-forget: the editor renders once the chunk resolves (guarded below).
    void import('./CodeMirrorEditor.svelte').then((m) => {
      CodeMirrorEditor = m.default;
    });
    loadGlobalEnv();
  });
</script>

<h4 class="mb-3 text-lg font-semibold text-white">
  <button
    class="flex items-center gap-2 text-zinc-300 hover:text-white"
    onclick={() => (showGlobalEnv = !showGlobalEnv)}
  >
    <span>{showGlobalEnv ? '▾' : '▸'}</span>
    Global Env
    <span class="text-xs font-normal text-zinc-500">(shared by all stacks on this instance)</span>
  </button>
</h4>
{#if showGlobalEnv}
  <div class="mb-4">
    <div class="mb-2">
      {#if CodeMirrorEditor}
        <CodeMirrorEditor
          value={globalEnv}
          placeholder="# KEY=value"
          onchange={(v) => { globalEnv = v; globalEnvDirty = true; }}
        />
      {:else}
        <div class="codemirror-host flex min-h-48 items-center justify-center rounded-md border border-zinc-700 bg-[#282a36] text-sm text-zinc-500">Loading editor…</div>
      {/if}
    </div>
    <button
      class="rounded-sm bg-zinc-700 px-3 py-1.5 text-sm text-zinc-100 hover:bg-zinc-600 disabled:opacity-50"
      disabled={!globalEnvDirty || processing}
      onclick={saveGlobalEnv}
    >
      💾 Save Global Env
    </button>
  </div>
{/if}
