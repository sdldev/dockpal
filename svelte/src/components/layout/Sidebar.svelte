<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import type { ComponentProps } from 'svelte';
  import { isAdmin, isOperator, sidebarOpen, systemUpdateBadge } from '../../lib/store';
  import { navigate } from '../../lib/router';
  import { getUpdateStatus, checkForUpdate } from '$lib/api/system';
  import { addToast } from '../../lib/store';
  import Icon from '../ui/Icon.svelte';
  import SystemUpdateModal from '../admin/SystemUpdateModal.svelte';

  interface Props {
    currentRoute?: string;
  }

  let { currentRoute = 'servers' }: Props = $props();

  // Update-check polling for the footer (release checks are infrequent, so a
  // 60s cadence is plenty). Failure leaves the previous summary untouched.
  let updateTimer: ReturnType<typeof setInterval> | null = null;
  let checking = $state(false);
  let showUpdateModal = $state(false);

  async function fetchUpdateSummary() {
    try {
      const s = await getUpdateStatus();
      systemUpdateBadge.set({
        currentVersion: s.current_version,
        updateAvailable: s.update_available,
        latestVersion: s.latest_version
      });
    } catch {
      // Keep the previous summary on transient errors.
    }
  }

  // Manual "check now" — forces a fresh upstream query and toasts the result.
  async function checkNow() {
    checking = true;
    try {
      const s = await checkForUpdate();
      systemUpdateBadge.set({
        currentVersion: s.current_version,
        updateAvailable: s.update_available,
        latestVersion: s.latest_version
      });
      if (s.update_available) {
        addToast(`Update available: v${s.latest_version}`, 'info');
      } else {
        addToast('Dockpal is up to date', 'success');
      }
    } catch (e) {
      addToast(e instanceof Error ? e.message : 'Check failed', 'error');
    } finally {
      checking = false;
    }
  }

  function onUpdateFinished(ok: boolean) {
    // Refresh the footer summary; after a successful update the running
    // version changes so the pill disappears on its own.
    if (ok) void fetchUpdateSummary();
  }

  onMount(() => {
    fetchUpdateSummary();
    updateTimer = setInterval(fetchUpdateSummary, 60000);
  });

  onDestroy(() => {
    if (updateTimer) clearInterval(updateTimer);
  });

  type MinRole = 'viewer' | 'operator' | 'admin';

  interface NavItem {
    id: string;
    label: string;
    icon: ComponentProps<typeof Icon>['name'];
    role?: MinRole; // undefined = everyone (viewer+)
    visible?: () => boolean; // undefined = always visible
  }

  const nav: NavItem[] = [
    { id: 'servers', label: 'Servers', icon: 'servers' },
    { id: 'stacks', label: 'Stacks', icon: 'stacks' },
    { id: 'containers', label: 'Containers', icon: 'containers' },
    { id: 'integrations', label: 'Integrations', icon: 'webhooks', role: 'operator' },
    { id: 'settings', label: 'Settings', icon: 'settings' }
  ];

  // Recomputed whenever roles change so role-gated items (Integrations for
  // operators+) appear/disappear live.
  const visibleNav = $derived(
    nav.filter((item) => {
      if (item.visible && !item.visible()) return false;
      if (!item.role) return true;
      if (item.role === 'admin') return $isAdmin;
      if (item.role === 'operator') return $isOperator;
      return true;
    })
  );

  // On mobile (< md, matching App.svelte) the sidebar is an overlay;
  // navigate should close it there. On desktop it pushes content instead.
  function handleNavigate(id: string) {
    navigate(id);
    if (typeof window !== 'undefined' && window.innerWidth < 768) {
      sidebarOpen.set(false);
    }
  }
</script>

<aside class="w-60 border-r border-zinc-800 bg-zinc-900 p-4 flex flex-col min-h-screen">
  <div class="mb-8">
    <h1 class="text-xl font-bold text-white">🐳 Dockpal</h1>
    <p class="text-xs text-zinc-500 mt-0.5">Docker Management</p>
  </div>

  <nav class="flex-1 space-y-1">
    {#each visibleNav as item (item.id)}
      {#if currentRoute === item.id}
        <button
          onclick={() => handleNavigate(item.id)}
          class="w-full flex items-center gap-3 px-3 py-2 rounded-sm text-sm transition-colors bg-zinc-800 text-white"
          aria-current={currentRoute === item.id ? 'page' : undefined}
        >
          <span class="shrink-0"><Icon name={item.icon} class="w-4.5 h-4.5" /></span>
          <span>{item.label}</span>
        </button>
      {:else}
        <button
          onclick={() => handleNavigate(item.id)}
          class="w-full flex items-center gap-3 px-3 py-2 rounded-sm text-sm transition-colors text-zinc-400 hover:text-white hover:bg-zinc-800"
        >
          <span class="shrink-0"><Icon name={item.icon} class="w-4.5 h-4.5" /></span>
          <span>{item.label}</span>
        </button>
      {/if}
    {/each}
  </nav>

  <div class="border-t border-zinc-800 pt-3 mt-4 space-y-2">
    {#if $systemUpdateBadge?.updateAvailable}
      {#if $isAdmin}
        <button
          onclick={() => (showUpdateModal = true)}
          class="w-full flex items-center gap-2 px-2.5 py-1.5 rounded-sm bg-emerald-500/10 border border-emerald-500/30 text-emerald-400 hover:bg-emerald-500/20 transition-colors text-xs"
          title={`Update Dockpal to v${$systemUpdateBadge.latestVersion}`}
        >
          <Icon name="download" class="w-3.5 h-3.5 shrink-0" />
          <span class="font-medium">Update to v{$systemUpdateBadge.latestVersion}</span>
        </button>
      {:else}
        <div
          class="flex items-center gap-2 px-2.5 py-1 text-xs text-zinc-500"
          title={`Dockpal v${$systemUpdateBadge.latestVersion} is available (ask an admin to update)`}
        >
          <Icon name="download" class="w-3.5 h-3.5 shrink-0" />
          <span>v{$systemUpdateBadge.latestVersion} available</span>
        </div>
      {/if}
    {/if}

    <div class="px-2.5 py-0.5 flex items-center justify-between gap-2">
      <span class="flex items-center gap-2 text-xs text-zinc-600 min-w-0" title="Dockpal version">
        <span aria-hidden="true">🐳</span>
        <span class="truncate">Dockpal</span>
        <span class="font-mono shrink-0">{$systemUpdateBadge?.currentVersion ? `v${$systemUpdateBadge.currentVersion}` : '—'}</span>
      </span>
      <button
        onclick={checkNow}
        disabled={checking}
        class="p-1 rounded-sm text-zinc-600 hover:text-white hover:bg-zinc-800 transition-colors disabled:opacity-50"
        title="Check for updates"
        aria-label="Check for updates"
      >
        <Icon name="refresh" class={`w-3.5 h-3.5 ${checking ? 'animate-spin' : ''}`} />
      </button>
    </div>
  </div>

  <SystemUpdateModal
    open={showUpdateModal}
    target={$systemUpdateBadge?.latestVersion ?? ''}
    current={$systemUpdateBadge?.currentVersion ?? ''}
    onfinished={onUpdateFinished}
    onclose={() => (showUpdateModal = false)}
  />
</aside>
