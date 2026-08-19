<script lang="ts">
  import { onMount } from 'svelte';
  import { api, getToken, clearToken } from './lib/api/client';
  import { currentUser } from './lib/store';
  import type { User } from './lib/types/api';
  import Login from './components/pages/Login.svelte';
  import Sidebar from './components/layout/Sidebar.svelte';
  import ToastContainer from './components/layout/ToastContainer.svelte';
  import Dashboard from './components/pages/Dashboard.svelte';
  import TemplatesPage from './components/pages/TemplatesPage.svelte';

  export let currentRoute = $state('dashboard');

  let initialized = false;

  async function loadUser() {
    const token = getToken();
    if (!token) {
      initialized = true;
      return;
    }
    try {
      const me = await api.get<User>('/profile');
      currentUser.set(me);
    } catch {
      currentUser.set(null);
      clearToken();
    } finally {
      initialized = true;
    }
  }

  function logout() {
    api.post('/logout').catch(() => {});
    clearToken();
    currentUser.set(null);
  }

  onMount(loadUser);
</script>

{#if !initialized}
  <div class="min-h-screen flex items-center justify-center text-zinc-500">Loading...</div>
{:else if !$currentUser}
  <Login />
{:else}
  <div class="flex min-h-screen">
    <Sidebar currentRoute={currentRoute} logout={logout} />
    <main class="flex-1 p-6 overflow-auto">
      {#if currentRoute === 'dashboard'}
        <Dashboard />
      {:else if currentRoute === 'templates'}
        <TemplatesPage />
      {:else if currentRoute === 'containers'}
        <div class="text-zinc-500">Containers list — coming in next iteration</div>
      {:else if currentRoute === 'images'}
        <div class="text-zinc-500">Images registry — coming in next iteration</div>
      {:else if currentRoute === 'services'}
        <div class="text-zinc-500">Services overview — coming in next iteration</div>
      {:else if currentRoute === 'settings'}
        <div class="text-zinc-500">Settings panel — coming in next iteration</div>
      {:else}
        <div class="text-zinc-500">Page not found</div>
      {/if}
    </main>
  </div>
{/if}

<ToastContainer />
