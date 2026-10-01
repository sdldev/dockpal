<script lang="ts">
  // Navheader — page title on the left, server status on the right.
  // Sits above the page content so individual pages no longer repeat their
  // own heading + description.
  import { onMount, onDestroy } from 'svelte';
  import { api } from '$lib/api/client';
  import { navTitle, navServerStatus, selectedInstance, sidebarOpen, systemUpdateBadge, isAdmin, type NavServerStatus } from '$lib/store';
  import { getUpdateStatus } from '$lib/api/system';
  import { navigate } from '$lib/router';
  import type { SystemInfo } from '$lib/types/api';
  import Icon from '../ui/Icon.svelte';

  let pollTimer: ReturnType<typeof setInterval> | null = null;
  let updatePollTimer: ReturnType<typeof setInterval> | null = null;
  let lastGood: NavServerStatus | null = null;

  function statusPath(instanceId: string): string {
    return instanceId === 'local' ? '/system/info' : `/instances/${encodeURIComponent(instanceId)}/system/info`;
  }

  async function fetchStatus() {
    try {
      const s = await api.get<SystemInfo>(statusPath($selectedInstance));
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

  // Poll system-update availability at a slower cadence. The badge only
  // surfaces for admins (they alone can act on it) and only when a newer
  // release is known — a null/empty badge renders nothing.
  async function fetchUpdateBadge() {
    try {
      const s = await getUpdateStatus();
      systemUpdateBadge.set({ updateAvailable: s.update_available, latestVersion: s.latest_version });
    } catch {
      // Leave the previous badge untouched on transient failures.
    }
  }

  onMount(() => {
    fetchStatus();
    pollTimer = setInterval(fetchStatus, 10000);
    fetchUpdateBadge();
    updatePollTimer = setInterval(fetchUpdateBadge, 60000);
  });

  onDestroy(() => {
    if (pollTimer) clearInterval(pollTimer);
    if (updatePollTimer) clearInterval(updatePollTimer);
  });
</script>

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
      <span class="inline text-zinc-600">•</span>
      <span class="inline capitalize">{$selectedInstance === 'local' ? 'This Server' : $selectedInstance}</span>
    {/if}
    {#if $isAdmin && $systemUpdateBadge?.updateAvailable}
      <button
        onclick={() => navigate('settings')}
        class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded-sm bg-emerald-500/10 border border-emerald-500/30 text-emerald-400 hover:bg-emerald-500/20 transition-colors"
        title={`Dockpal v${$systemUpdateBadge.latestVersion} is available — open Settings to update`}
      >
        <Icon name="download" class="w-3 h-3" />
        <span class="hidden sm:inline">v{$systemUpdateBadge.latestVersion}</span>
      </button>
    {/if}
  </div>
</header>
