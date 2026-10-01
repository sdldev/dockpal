<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    open: boolean;
    title: string;
    size?: 'sm' | 'md' | 'lg' | 'xl';
    onclose: () => void;
    children?: Snippet;
  }

  let { open, title, size = 'md', onclose, children }: Props = $props();

  const sizeClasses: Record<string, string> = {
    sm: 'max-w-md',
    md: 'max-w-lg',
    lg: 'max-w-2xl',
    xl: 'max-w-4xl'
  };

  // Unique per instance so multiple modals never share an aria-labelledby id.
  const titleId = `modal-title-${Math.random().toString(36).slice(2, 9)}`;

  let panel: HTMLDivElement | null = $state(null);
  let previouslyFocused: Element | null = null;

  // Focus management: move focus into the dialog on open, restore it on
  // close (audit-stack-container L7).
  $effect(() => {
    if (open) {
      previouslyFocused = document.activeElement;
      // Defer so the dialog DOM exists before focusing.
      queueMicrotask(() => panel?.focus());
      return () => {
        if (previouslyFocused instanceof HTMLElement) previouslyFocused.focus();
        previouslyFocused = null;
      };
    }
  });

  function overlayClick(event: MouseEvent) {
    if (event.target === event.currentTarget) onclose();
  }

  function keydown(event: KeyboardEvent) {
    if (!open) return;
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
    <div
      bind:this={panel}
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      tabindex="-1"
      class="bg-zinc-900 border border-zinc-800 rounded-sm w-full {sizeClasses[size]} max-h-[90vh] overflow-y-auto focus:outline-none"
    >
      <div class="flex items-center justify-between p-4 border-b border-zinc-800">
        <h3 id={titleId} class="text-base font-semibold text-white">{title}</h3>
        <button onclick={onclose} class="text-zinc-400 hover:text-white" aria-label="Close">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"></path>
          </svg>
        </button>
      </div>
      <div class="p-4">
        {@render children?.()}
      </div>
    </div>
  </div>
{/if}
