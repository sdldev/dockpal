<script lang="ts">
  import { currentUser, currentPage } from '../../lib/store';
  import { api, clearToken } from '../../lib/api/client';

  interface Props {
    currentRoute?: string;
    logout?: () => void;
  }

  let { currentRoute = 'dashboard', logout }: Props = $props();

  const nav = [
    { id: 'dashboard', label: 'Dashboard', icon: '📊' },
    { id: 'containers', label: 'Containers', icon: '🐳' },
    { id: 'templates', label: 'Templates', icon: '📦' },
    { id: 'images', label: 'Images', icon: '🗄️' },
    { id: 'services', label: 'Services', icon: '🧩' },
    { id: 'settings', label: 'Settings', icon: '⚙️' }
  ];

  async function handleLogout() {
    if (logout) {
      logout();
    } else {
      try { await api.post('/logout'); } catch {}
      clearToken();
      currentUser.set(null);
    }
  }
</script>

<aside class="w-60 border-r border-zinc-800 bg-zinc-900 p-4 flex flex-col min-h-screen">
  <div class="mb-8">
    <h1 class="text-xl font-bold text-white">🐳 Dockpal</h1>
    <p class="text-xs text-zinc-500 mt-0.5">Docker Management</p>
  </div>

  <nav class="flex-1 space-y-1">
    {#each nav as item}
      <button
        on:click={() => currentPage.set(item.id)}
        class="w-full flex items-center gap-3 px-3 py-2 rounded-sm text-sm transition-colors"
        class:bg-zinc-800={currentRoute === item.id}
        class:text-white={currentRoute === item.id}
        class:text-zinc-400={currentRoute !== item.id}
      >
        <span>{item.icon}</span>
        <span>{item.label}</span>
      </button>
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
