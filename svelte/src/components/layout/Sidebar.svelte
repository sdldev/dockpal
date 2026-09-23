<script lang="ts">
  import { onMount } from 'svelte';
  import { currentUser, isAdmin, isOperator, selectedInstance } from '../../lib/store';
  import { navigate } from '../../lib/router';
  import { listInstances } from '$lib/api/stacks';
  import type { InstanceListItem } from '$lib/types/api';

  interface Props {
    currentRoute?: string;
    logout?: () => void;
  }

  let { currentRoute = 'dashboard', logout }: Props = $props();

  // Instance list for the server selector (legacy sidebar parity)
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
    icon: string;
    role?: MinRole; // undefined = everyone (viewer+)
  }

  const nav: NavItem[] = [
    { id: 'dashboard', label: 'Dashboard', icon: '📊' },
    { id: 'fleet', label: 'Fleet', icon: '🛰️' },
    { id: 'stacks', label: 'Compose Stacks', icon: '🗂' },
    { id: 'containers', label: 'Containers', icon: '🐳' },
    { id: 'images', label: 'Images', icon: '🗄️' },
    { id: 'services', label: 'Services', icon: '🧩' },
    { id: 'templates', label: 'App Installer', icon: '📦' },
    { id: 'webhooks', label: 'Webhooks', icon: '🪝', role: 'operator' },
    { id: 'domains', label: 'Domains', icon: '🌐', role: 'operator' },
    { id: 'apps', label: 'Installed Apps', icon: '🚀', role: 'viewer' },
    { id: 'settings', label: 'Settings', icon: '⚙️' },
    { id: 'admin', label: 'Admin', icon: '🛡️', role: 'admin' }
  ];

  function canSee(item: NavItem, admin: boolean, operator: boolean): boolean {
    if (!item.role) return true;
    if (item.role === 'admin') return admin;
    if (item.role === 'operator') return operator;
    return true;
  }

  async function handleLogout() {
    logout?.();
  }
</script>

<aside class="w-60 border-r border-zinc-800 bg-zinc-900 p-4 flex flex-col min-h-screen">
  <div class="mb-8">
    <h1 class="text-xl font-bold text-white">🐳 Dockpal</h1>
    <p class="text-xs text-zinc-500 mt-0.5">Docker Management</p>
  </div>

  <!-- Instance / server selector (legacy sidebar parity) -->
  <div class="px-1 pb-4 mb-2 border-b border-zinc-800">
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
  </div>

  <nav class="flex-1 space-y-1">
    {#each nav.filter((item) => canSee(item, $isAdmin, $isOperator)) as item}
      {#if currentRoute === item.id}
        <button
          onclick={() => navigate(item.id)}
          class="w-full flex items-center gap-3 px-3 py-2 rounded-sm text-sm transition-colors bg-zinc-800 text-white"
        >
          <span>{item.icon}</span>
          <span>{item.label}</span>
        </button>
      {:else}
        <button
          onclick={() => navigate(item.id)}
          class="w-full flex items-center gap-3 px-3 py-2 rounded-sm text-sm transition-colors text-zinc-400 hover:text-white hover:bg-zinc-800"
        >
          <span>{item.icon}</span>
          <span>{item.label}</span>
        </button>
      {/if}
    {/each}
  </nav>

  <div class="border-t border-zinc-800 pt-4 mt-4">
    {#if $currentUser}
      <div class="px-3 mb-3">
        <div class="text-sm font-medium text-white">{$currentUser.username}</div>
        <div class="text-xs text-zinc-500 capitalize">{$currentUser.role}</div>
      </div>
      <button
        onclick={handleLogout}
        class="w-full px-3 py-2 text-left text-sm text-zinc-400 hover:text-white rounded-sm hover:bg-zinc-800 transition-colors"
      >
        Logout
      </button>
    {:else}
      <a href="/login" class="block text-center px-3 py-2 rounded-sm bg-white text-zinc-900 hover:bg-zinc-200 transition-colors mt-4">
        Login
      </a>
    {/if}
  </div>
</aside>
