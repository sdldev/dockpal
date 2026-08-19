<script lang="ts">
  import { onMount } from 'svelte';
  import { api, getToken } from './lib/api/client';
  import { currentUser, currentPage } from './lib/store';
  import type { User } from './lib/types/api';
  import Login from './components/Login.svelte';
  import Sidebar from './components/layout/Sidebar.svelte';
  import ToastContainer from './components/layout/ToastContainer.svelte';
  import Dashboard from './components/pages/Dashboard.svelte';
  import TemplatesPage from './components/pages/TemplatesPage.svelte';

  let loaded = false;
  let page = '';

  currentPage.subscribe((value) => {
    page = value;
  });

  onMount(async () => {
    if (!getToken()) {
      loaded = true;
      return;
    }
    try {
      const me = await api.get<User>('/profile');
      currentUser.set(me);
    } catch {
      currentUser.set(null);
    } finally {
      loaded = true;
    }
  });
</script>

{#if !loaded}
  <div class="min-h-screen flex items-center justify-center text-zinc-500">Loading...</div>
{:else if !$currentUser}
  <Login />
{:else}
  <div class="flex min-h-screen">
    <Sidebar />
    <main class="flex-1 p-6 overflow-auto">
      {#if page === 'dashboard'}
        <Dashboard />
      {:else if page === 'templates'}
        <TemplatesPage />
      {:else}
        <div class="text-zinc-500">Page "{page}" — coming in next phase</div>
      {/if}
    </main>
  </div>
{/if}

<ToastContainer />
