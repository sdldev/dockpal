<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../lib/api/client';
  import { templates, addToast } from '../../lib/store';
  import type { Template } from '../../lib/types/api';

  let loading = true;

  onMount(async () => {
    try {
      const list = await api.get<Template[]>('/templates');
      templates.set(list);
    } catch {
      addToast('Failed to load templates', 'error');
    } finally {
      loading = false;
    }
  });
</script>

<div class="space-y-6">
  <div>
    <h2 class="text-lg font-semibold text-white">Templates</h2>
    <p class="text-sm text-zinc-500">Deploy stacks from predefined templates</p>
  </div>

  <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
    {#each $templates as tpl (tpl.id)}
      <div class="bg-zinc-900 border border-zinc-800 rounded-sm p-5 hover:border-zinc-700 transition-colors">
        <div class="flex items-center gap-3 mb-2">
          <span class="text-3xl">{tpl.icon}</span>
          <div>
            <h3 class="text-sm font-semibold text-white">{tpl.name}</h3>
            <span class="text-xs text-zinc-500 capitalize">{tpl.category}</span>
          </div>
        </div>
        <p class="text-xs text-zinc-400 mb-4">{tpl.description}</p>
        <button
          on:click={() => addToast(`Deploy ${tpl.name} — wizard comes in Phase 2`, 'info')}
          class="w-full py-2 bg-white hover:bg-zinc-200 text-zinc-900 rounded-sm text-xs font-medium transition-colors"
        >
          Deploy
        </button>
      </div>
    {:else}
      <div class="col-span-full text-center py-12 text-zinc-600 text-sm">
        {loading ? 'Loading templates...' : 'No templates available'}
      </div>
    {/each}
  </div>
</div>
