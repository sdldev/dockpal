<script lang="ts">
  import { toasts, removeToast } from '../../lib/store';

  function typeClasses(type: string): string {
    switch (type) {
      case 'success':
        return 'bg-emerald-600 border-emerald-500';
      case 'error':
        return 'bg-red-600 border-red-500';
      default:
        return 'bg-zinc-800 border-zinc-700';
    }
  }
</script>

<div class="fixed bottom-4 right-4 z-50 space-y-2">
  {#each $toasts as toast (toast.id)}
    <div class="px-4 py-3 rounded-sm border shadow-lg text-sm text-white flex items-center gap-3 {typeClasses(toast.type)}">
      <span>{toast.message}</span>
      <button on:click={() => removeToast(toast.id)} class="opacity-70 hover:opacity-100">✕</button>
    </div>
  {/each}
</div>
