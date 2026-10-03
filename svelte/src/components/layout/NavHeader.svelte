<script lang="ts">
  // Navheader — page title on the left, server status + profile menu on the
  // right. Sits above the page content so individual pages no longer repeat
  // their own heading + description.
  import { onMount, onDestroy } from 'svelte';
  import { api } from '$lib/api/client';
  import { navTitle, navServerStatus, selectedInstance, sidebarOpen, currentUser, type NavServerStatus } from '$lib/store';
  import { navigate } from '$lib/router';
  import { listInstances } from '$lib/api/stacks';
  import type { InstanceListItem } from '$lib/types/api';
  import type { SystemInfo } from '$lib/types/api';
  import Icon from '../ui/Icon.svelte';

  interface Props {
    logout?: () => void;
  }
  let { logout }: Props = $props();

  let pollTimer: ReturnType<typeof setInterval> | null = null;
  let lastGood: NavServerStatus | null = null;

  function statusPath(instanceId: string): string {
    return instanceId === 'local' ? '/system/info' : `/instances/${encodeURIComponent(instanceId)}/system/info`;
  }

  async function fetchStatus(instanceId: string) {
    try {
      const s = await api.get<SystemInfo>(statusPath(instanceId));
      lastGood = {
        hostname: s.hostname,
        os: s.os,
        dockerVersion: s.docker_version,
        cpuCores: s.cpu_cores,
        online: true
      };
      navServerStatus.set(lastGood);
    } catch {
      // Keep last known values, mark offline
      if (lastGood) {
        lastGood = { ...lastGood, online: false };
        navServerStatus.set(lastGood);
      }
    }
  }

  // Refetch immediately when the selected server changes, dropping the stale
  // "last good" values so the chip never shows the previous server's data;
  // the 10s interval keeps it fresh afterwards.
  let lastStatusId = '';
  $effect(() => {
    const id = $selectedInstance;
    if (id !== lastStatusId) {
      lastStatusId = id;
      lastGood = null;
      navServerStatus.set(null);
    }
    void fetchStatus(id);
  });

  onMount(() => {
    void loadServers();
    pollTimer = setInterval(() => void fetchStatus($selectedInstance), 10000);
  });

  onDestroy(() => {
    if (pollTimer) clearInterval(pollTimer);
  });

  // Server switcher — the status chip doubles as the quick server switcher
  // (the sidebar selector was removed when server context moved into the URL
  // as /servers/:id). Picking a server navigates to its control panel.
  let serversList = $state<InstanceListItem[]>([]);
  let serverMenuOpen = $state(false);
  let serverMenuWrap: HTMLDivElement | undefined = $state();

  function serverLabel(inst: InstanceListItem): string {
    return inst.id === 'local' ? 'This Server' : inst.name;
  }

  function serverOnline(inst: InstanceListItem): boolean {
    return inst.id === 'local' || inst.status === 'online';
  }

  async function loadServers() {
    try {
      const res = await listInstances();
      const list = Array.isArray(res) ? res : (res.instances ?? []);
      // Guarantee the local daemon is always listed, even if the API omits it.
      serversList = list.some((i) => i.id === 'local')
        ? list
        : [
            { id: 'local', name: 'This Server', host: '', port: 0, mode: 'local', status: 'online', last_seen: 0 },
            ...list
          ];
    } catch {
      serversList = [];
    }
  }

  function toggleServerMenu() {
    serverMenuOpen = !serverMenuOpen;
    if (serverMenuOpen) void loadServers();
  }

  function switchServer(id: string) {
    serverMenuOpen = false;
    if (id === $selectedInstance) return;
    selectedInstance.set(id);
    localStorage.setItem('dockpal_selected_instance', id);
    navigate('server-detail', { id });
  }

  const currentServerLabel = $derived.by(() => {
    const inst = serversList.find((i) => i.id === $selectedInstance);
    return inst ? serverLabel(inst) : $selectedInstance === 'local' ? 'This Server' : $selectedInstance;
  });

  // Profile dropdown (username / role / logout). Closed by picking an item,
  // clicking anywhere outside, or pressing Escape.
  let profileOpen = $state(false);
  let profileWrap: HTMLDivElement | undefined = $state();

  function toggleProfile() {
    profileOpen = !profileOpen;
  }

  function handleWindowPointerDown(e: PointerEvent) {
    if (serverMenuOpen && serverMenuWrap && !serverMenuWrap.contains(e.target as Node)) {
      serverMenuOpen = false;
    }
    if (profileOpen && profileWrap && !profileWrap.contains(e.target as Node)) {
      profileOpen = false;
    }
  }

  function handleWindowKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      serverMenuOpen = false;
      profileOpen = false;
    }
  }

  function gotoSettings() {
    profileOpen = false;
    navigate('settings');
  }

  function doLogout() {
    profileOpen = false;
    logout?.();
  }

  const initial = $derived(($currentUser?.username ?? '?').charAt(0).toUpperCase());
</script>

<svelte:window onpointerdown={handleWindowPointerDown} onkeydown={handleWindowKeydown} />

<header
  class="sticky top-0 z-30 flex items-center justify-between gap-3 px-4 sm:px-6 h-14 border-b border-zinc-800 bg-zinc-950/80 backdrop-blur-sm"
>
  <div class="flex items-center gap-3 min-w-0">
    <button
      onclick={() => sidebarOpen.update((v) => !v)}
      class="p-2 rounded-sm text-zinc-400 hover:text-white hover:bg-zinc-800 transition-colors"
      aria-label="Toggle sidebar"
      aria-expanded={$sidebarOpen}
    >
      <Icon name="menu" class="w-5 h-5" />
    </button>
    <h1 class="text-base font-semibold text-white truncate">{$navTitle}</h1>
  </div>

  <div class="flex items-center gap-3 text-xs text-zinc-400 shrink-0">
    {#if $navServerStatus}
      <div class="relative" bind:this={serverMenuWrap}>
        <button
          onclick={toggleServerMenu}
          class="flex items-center gap-1.5 px-2 py-1 -my-1 rounded-sm border border-transparent hover:border-zinc-800 hover:bg-zinc-900 transition-colors"
          aria-haspopup="menu"
          aria-expanded={serverMenuOpen}
          title="Switch server"
        >
          <span class="hidden sm:flex items-center gap-1.5">
            <span
              class="w-1.5 h-1.5 rounded-full"
              class:bg-emerald-400={$navServerStatus.online}
              class:bg-red-400={!$navServerStatus.online}
              aria-hidden="true"
            ></span>
            <span class="font-medium text-zinc-300 truncate max-w-40" title={$navServerStatus.hostname}>
              {$navServerStatus.hostname}
            </span>
          </span>
          <span class="hidden md:inline text-zinc-600">•</span>
          <span class="hidden md:inline">Docker {$navServerStatus.dockerVersion}</span>
          <span class="hidden lg:inline text-zinc-600">•</span>
          <span class="hidden lg:inline">{$navServerStatus.cpuCores} cores</span>
          <span class="hidden sm:inline text-zinc-600">•</span>
          <span class="inline text-zinc-300">{currentServerLabel}</span>
          <Icon name="chevron-down" class="w-3 h-3 text-zinc-500" />
        </button>

        {#if serverMenuOpen}
          <div
            class="absolute right-0 top-full mt-2 w-60 bg-zinc-900 border border-zinc-800 rounded-sm shadow-xl py-2 z-40"
            role="menu"
          >
            <div
              class="px-3 pb-2 border-b border-zinc-800 text-[10px] font-semibold text-zinc-500 uppercase tracking-wider"
            >
              Servers
            </div>
            {#each serversList as inst (inst.id)}
              <button
                onclick={() => switchServer(inst.id)}
                class="w-full flex items-center gap-2.5 px-3 py-2 text-left text-sm transition-colors {inst.id === $selectedInstance ? 'bg-zinc-800/60 text-white' : 'text-zinc-300 hover:text-white hover:bg-zinc-800'}"
                role="menuitem"
              >
                <span
                  class="w-1.5 h-1.5 rounded-full shrink-0 {serverOnline(inst) ? 'bg-emerald-400' : 'bg-zinc-600'}"
                  aria-hidden="true"
                ></span>
                <span class="truncate">{serverLabel(inst)}</span>
                {#if inst.id === $selectedInstance}
                  <span class="ml-auto text-[10px] uppercase font-semibold text-zinc-500">current</span>
                {/if}
              </button>
            {:else}
              <div class="px-3 py-2 text-xs text-zinc-600">No servers found</div>
            {/each}
          </div>
        {/if}
      </div>
    {/if}

    {#if $currentUser}
      <div class="relative" bind:this={profileWrap}>
        <button
          onclick={toggleProfile}
          class="flex items-center gap-2 pl-1 pr-2 py-1 rounded-full border border-zinc-800 bg-zinc-900 hover:bg-zinc-800 transition-colors"
          aria-haspopup="menu"
          aria-expanded={profileOpen}
          title="Account menu"
        >
          <span
            class="w-6 h-6 rounded-full bg-blue-600 text-white text-xs font-semibold flex items-center justify-center"
            aria-hidden="true"
          >
            {initial}
          </span>
          <span class="hidden sm:inline text-xs font-medium text-zinc-200 max-w-28 truncate">
            {$currentUser.username}
          </span>
          <Icon name="chevron-down" class="w-3 h-3 text-zinc-500" />
        </button>

        {#if profileOpen}
          <div
            class="absolute right-0 top-full mt-2 w-52 bg-zinc-900 border border-zinc-800 rounded-sm shadow-xl py-2 z-40"
            role="menu"
          >
            <div class="px-3 pb-2 border-b border-zinc-800">
              <div class="text-sm font-medium text-white truncate">{$currentUser.username}</div>
              <div class="text-xs text-zinc-500 capitalize">{$currentUser.role}</div>
            </div>
            <button
              onclick={gotoSettings}
              class="w-full flex items-center gap-2.5 px-3 py-2 text-left text-sm text-zinc-300 hover:text-white hover:bg-zinc-800 transition-colors"
              role="menuitem"
            >
              <Icon name="settings" class="w-4 h-4" />
              <span>Settings</span>
            </button>
            <button
              onclick={doLogout}
              class="w-full flex items-center gap-2.5 px-3 py-2 text-left text-sm text-red-400 hover:text-red-300 hover:bg-zinc-800 transition-colors"
              role="menuitem"
            >
              <Icon name="logout" class="w-4 h-4" />
              <span>Logout</span>
            </button>
          </div>
        {/if}
      </div>
    {/if}
  </div>
</header>
