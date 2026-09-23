<script lang="ts">
  import { onMount } from 'svelte';
  import { api, getToken, clearToken } from './lib/api/client';
  import { currentUser, currentPage, isAdmin, selectedInstance } from './lib/store';
  import { initRouter, navigate } from './lib/router';
  import type { User } from './lib/types/api';
  import Login from './components/pages/Login.svelte';
  import Sidebar from './components/layout/Sidebar.svelte';
  import ToastContainer from './components/layout/ToastContainer.svelte';
  import Dashboard from './components/pages/Dashboard.svelte';
  import TemplatesPage from './components/pages/TemplatesPage.svelte';
  import ContainersPage from './components/pages/ContainersPage.svelte';
  import ImagesPage from './components/pages/ImagesPage.svelte';
  import ServicesPage from './components/pages/ServicesPage.svelte';
  import SettingsPage from './components/pages/SettingsPage.svelte';
  import AdminPage from './components/pages/AdminPage.svelte';
  import WebhooksPage from './components/pages/WebhooksPage.svelte';
  import DomainsPage from './components/pages/DomainsPage.svelte';
  import AppsPage from './components/pages/AppsPage.svelte';
  import ContainerPage from './components/pages/ContainerPage.svelte';
  import StacksPage from './components/pages/StacksPage.svelte';
  import ComposePage from './components/pages/ComposePage.svelte';
  import FleetPage from './components/pages/FleetPage.svelte';

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
    navigate('dashboard', {}, true);
  }

  onMount(() => {
    loadUser();
    initRouter();
    // Global 401 handler: any API call with an expired/revoked token
    // dispatches this event so we drop the session back to login.
    const onUnauthorized = () => {
      currentUser.set(null);
      navigate('dashboard', {}, true);
    };
    window.addEventListener('dockpal:unauthorized', onUnauthorized);
    return () => window.removeEventListener('dockpal:unauthorized', onUnauthorized);
  });
</script>

{#if !initialized}
  <div class="min-h-screen flex items-center justify-center text-zinc-500">Loading...</div>
{:else if !$currentUser}
  <Login />
{:else}
  <div class="flex min-h-screen">
    <Sidebar currentRoute={$currentPage} logout={logout} />
    <main class="flex-1 p-6 overflow-auto">
      <!-- Remount the current page when the selected instance changes so
           instance-scoped pages (Dashboard, Stacks, Compose...) re-fetch
           instead of showing stale data from the previous server. -->
      {#key $selectedInstance}
        {#if $currentPage === 'dashboard'}
          <Dashboard />
        {:else if $currentPage === 'fleet'}
          <FleetPage />
        {:else if $currentPage === 'templates'}
          <TemplatesPage />
        {:else if $currentPage === 'containers'}
          <ContainersPage />
        {:else if $currentPage === 'images'}
          <ImagesPage />
        {:else if $currentPage === 'services'}
          <ServicesPage />
        {:else if $currentPage === 'settings'}
          <SettingsPage />
        {:else if $currentPage === 'admin'}
          {#if $isAdmin}
            <AdminPage />
          {:else}
            <div class="text-zinc-500">Access denied — admin only</div>
          {/if}
        {:else if $currentPage === 'webhooks'}
          <WebhooksPage />
        {:else if $currentPage === 'domains'}
          <DomainsPage />
        {:else if $currentPage === 'apps'}
          <AppsPage />
        {:else if $currentPage === 'stacks'}
          <StacksPage />
        {:else if $currentPage === 'compose'}
          <ComposePage />
        {:else if $currentPage === 'container-detail'}
          <ContainerPage />
        {:else}
          <div class="text-zinc-500">Page not found</div>
        {/if}
      {/key}
    </main>
  </div>
{/if}

<ToastContainer />
