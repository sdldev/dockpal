<script lang="ts">
  interface Props {
    open: boolean;
    title: string;
    size?: 'sm' | 'md' | 'lg' | 'xl';
    onclose: () => void;
  }

  let { open, title, size = 'md', onclose }: Props = $props();

  const sizeClasses: Record<string, string> = {
    sm: 'max-w-md',
    md: 'max-w-lg',
    lg: 'max-w-2xl',
    xl: 'max-w-4xl'
  };

  function overlayClick(event: MouseEvent) {
    if (event.target === event.currentTarget) onclose();
  }

  function keydown(event: KeyboardEvent) {
    if (event.key === 'Escape') onclose();
  }
</script>

<svelte:window onkeydown={keydown} />

{#if open}
  <div
    class="fixed inset-0 bg-black/60 z-50 flex items-center justify-center p-4"
    onclick={overlayClick}
    role="presentation"
  >
    <div class="bg-zinc-900 border border-zinc-800 rounded-sm w-full {sizeClasses[size]} max-h-[90vh] overflow-y-auto">
      <div class="flex items-center justify-between p-4 border-b border-zinc-800">
        <h3 class="text-base font-semibold text-white">{title}</h3>
        <button onclick={onclose} class="text-zinc-400 hover:text-white" aria-label="Close">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"></path>
          </svg>
        </button>
      </div>
      <div class="p-4">
        <slot />
      </div>
    </div>
  </div>
{/if}
