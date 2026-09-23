<script lang="ts">
  // Reusable confirmation dialog for destructive actions. Every delete /
  // teardown / prune path in the app should route through this so a stray
  // click can never remove data without an explicit second confirmation.
  import Modal from './Modal.svelte';
  import Button from './Button.svelte';

  interface Props {
    open: boolean;
    title: string;
    message: string;
    confirmLabel?: string;
    busy?: boolean;
    onconfirm: () => void;
    onclose: () => void;
  }

  let {
    open,
    title,
    message,
    confirmLabel = 'Delete',
    busy = false,
    onconfirm,
    onclose
  }: Props = $props();
</script>

<Modal {open} {title} size="sm" {onclose}>
  <p class="text-sm text-zinc-300">{message}</p>
  <div class="mt-4 flex justify-end gap-2">
    <Button variant="secondary" disabled={busy} onclick={onclose}>Cancel</Button>
    <Button variant="danger" loading={busy} onclick={onconfirm}>{confirmLabel}</Button>
  </div>
</Modal>
