<script lang="ts">
  interface Props {
    variant?: 'primary' | 'secondary' | 'danger';
    size?: 'sm' | 'md' | 'lg';
    disabled?: boolean;
    loading?: boolean;
    type?: 'button' | 'submit' | 'reset';
    class?: string;
    onclick?: (event: MouseEvent) => void;
  }

  let {
    variant = 'primary',
    size = 'md',
    disabled = false,
    loading = false,
    type = 'button',
    class: extraClass = '',
    onclick
  }: Props = $props();

  const variantClasses: Record<string, string> = {
    primary: 'bg-white text-zinc-900 hover:bg-zinc-200',
    secondary: 'bg-zinc-800 text-white hover:bg-zinc-700 border border-zinc-700',
    danger: 'bg-red-600 text-white hover:bg-red-700'
  };

  const sizeClasses: Record<string, string> = {
    sm: 'px-3 py-1.5 text-xs',
    md: 'px-4 py-2 text-sm',
    lg: 'px-6 py-3 text-base'
  };

  const classes = () =>
    [
      'font-medium rounded-sm transition-all active:scale-[0.98] inline-flex items-center justify-center gap-2',
      variantClasses[variant],
      sizeClasses[size],
      disabled || loading ? 'opacity-50 cursor-not-allowed' : '',
      extraClass
    ]
      .filter(Boolean)
      .join(' ');
</script>

<button {type} class={classes()} disabled={disabled || loading} {onclick}>
  {#if loading}
    <svg class="animate-spin h-4 w-4" viewBox="0 0 24 24" fill="none">
      <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
      <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"></path>
    </svg>
  {/if}
  <slot />
</button>
