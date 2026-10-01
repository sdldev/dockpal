<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import type { ComponentProps } from 'svelte';
  import { isAdmin, isOperator, selectedInstance, sidebarOpen, systemUpdateBadge, adminInitialTab } from '../../lib/store';
  import { navigate } from '../../lib/router';
  import { listInstances } from '$lib/api/stacks';
  import { getUpdateStatus } from '$lib/api/system';
  import type { InstanceListItem } from '$lib/types/api';
  import Icon from '../ui/Icon.svelte';

  interface Props {
    currentRoute?: string;
  }

  let { currentRoute = 'dashboard' }: Props = $props();

  // Instance list for the server selector — also drives Servers visibility:
  // single-server users (the majority) never need it, so the item only
  // appears once a second instance exists.
  let instances = $state<InstanceListItem[]>([]);

  onMount(async () => {
    try {
      const res = await listInstances();
      const list = Array.isArray(res) ? res : (res.instances ?? []);
      // Guarantee the local daemon is always selectable, even if the API
      // response omits it (legacy state.js loadInstances does the same).
      instances = list.some((i) => i.id === 'local')
        ? list
        : [
            { id: 'local', name: 'This Server', host: '', port: 0, mode: 'local', status: 'online', last_seen: 0 },
            ...list
          ];
    } catch {
      instances = [];
    }
  });

  // Update-check polling for the footer (release checks are infrequent, so a
  // 60s cadence is plenty). Failure leaves the previous summary untouched.
  let updateTimer: ReturnType<typeof setInterval> | null = null;

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

  onMount(() => {
    fetchUpdateSummary();
    updateTimer = setInterval(fetchUpdateSummary, 60000);
  });

  onDestroy(() => {
    if (updateTimer) clearInterval(updateTimer);
  });

  // Shortcut from the footer's "update available" pill straight to
  // Settings → Administration → Update.
  function gotoUpdate() {
    adminInitialTab.set('update');
    navigate('settings');
    if (typeof window !== 'undefined' && window.innerWidth < 768) {
      sidebarOpen.set(false);
    }
  }

  const showFleet = $derived(instances.length > 1);

  function onInstanceChange(e: Event) {
    const id = (e.target as HTMLSelectElement).value;
    selectedInstance.set(id);
  }

  function instanceIcon(inst: InstanceListItem): string {
    if (inst.id === 'local') return '⚙️';
    if (inst.status === 'online') return '🟢';
    if (inst.status === 'offline') return '🔴';
    return '🟡';
  }

  function instanceLabel(inst: InstanceListItem): string {
    return inst.id === 'local' ? 'This Server' : inst.name;
  }

  type MinRole = 'viewer' | 'operator' | 'admin';

  interface NavItem {
    id: string;
    label: string;
    icon: ComponentProps<typeof Icon>['name'];
    role?: MinRole; // undefined = everyone (viewer+)
    visible?: () => boolean; // undefined = always visible
  }

  const nav: NavItem[] = [
    { id: 'dashboard', label: 'Dashboard', icon: 'dashboard' },
    { id: 'fleet', label: 'Servers', icon: 'fleet', visible: () => showFleet },
    { id: 'stacks', label: 'Stacks', icon: 'stacks' },
    { id: 'containers', label: 'Containers', icon: 'containers' },
    { id: 'integrations', label: 'Integrations', icon: 'webhooks', role: 'operator' },
    { id: 'settings', label: 'Settings', icon: 'settings' }
  ];

  // Recomputed whenever instances/roles change so Servers appears/disappears
  // live as remote instances are added or removed.
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

  <!-- Instance / server selector — only shown once a second server exists
       (single-server users don't need to "choose" anything). The "+ Add
       Server" link is the discoverable entry point to the multi-server flow. -->
  <div class="px-1 pb-4 mb-2 border-b border-zinc-800">
    {#if showFleet}
      <select
        aria-label="Select server"
        value={$selectedInstance}
        onchange={onInstanceChange}
        class="w-full px-2 py-1.5 bg-zinc-950 border border-zinc-800 rounded-sm text-xs text-white focus:outline-none focus:ring-2 focus:ring-blue-600 cursor-pointer"
      >
        {#each instances as inst (inst.id)}
          <option value={inst.id}>{instanceIcon(inst)} {instanceLabel(inst)}</option>
        {:else}
          <option value="local">⚙️ This Server</option>
        {/each}
      </select>
    {/if}
    {#if $isAdmin && !showFleet}
      <button
        onclick={() => navigate('fleet')}
        class="w-full flex items-center justify-center gap-1.5 px-2 py-1.5 rounded-sm text-xs text-zinc-500 hover:text-white hover:bg-zinc-800 border border-dashed border-zinc-800 hover:border-zinc-600 transition-colors"
      >
        <span class="text-sm leading-none">+</span> Add Server
      </button>
    {/if}
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

  <div class="border-t border-zinc-800 pt-3 mt-4 space-y-1.5">
    {#if $systemUpdateBadge?.updateAvailable && $isAdmin}
      <button
        onclick={gotoUpdate}
        class="w-full flex items-center gap-2 px-2.5 py-1.5 rounded-sm bg-emerald-500/10 border border-emerald-500/30 text-emerald-400 hover:bg-emerald-500/20 transition-colors text-xs"
        title={`Dockpal v${$systemUpdateBadge.latestVersion} is available — open the updater`}
      >
        <Icon name="download" class="w-3.5 h-3.5 shrink-0" />
        <span class="font-medium">Update to v{$systemUpdateBadge.latestVersion}</span>
      </button>
    {:else if $systemUpdateBadge?.updateAvailable}
      <div
        class="flex items-center gap-2 px-2.5 py-1.5 rounded-sm text-xs text-zinc-500"
        title={`Dockpal v${$systemUpdateBadge.latestVersion} is available (ask an admin to update)`}
      >
        <Icon name="download" class="w-3.5 h-3.5 shrink-0" />
        <span>v{$systemUpdateBadge.latestVersion} available</span>
      </div>
    {/if}
    <div class="px-2.5 flex items-center gap-2 text-xs text-zinc-600">
      <span>🐳</span>
      <span>Dockpal</span>
      <span class="font-mono">{$systemUpdateBadge?.currentVersion ? `v${$systemUpdateBadge.currentVersion}` : '—'}</span>
    </div>
  </div>
</aside>
