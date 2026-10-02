<script lang="ts">
  import { onMount } from 'svelte';
  import { api, getToken, clearToken } from './lib/api/client';
  import { currentUser, currentPage, selectedInstance, sidebarOpen, navTitle } from './lib/store';
  import { initRouter, navigate } from './lib/router';
  import type { User } from './lib/types/api';
  import Login from './components/pages/Login.svelte';
  import Sidebar from './components/layout/Sidebar.svelte';
  import ToastContainer from './components/layout/ToastContainer.svelte';
  import Dashboard from './components/pages/Dashboard.svelte';
  import ContainersPage from './components/pages/ContainersPage.svelte';
  import SettingsPage from './components/pages/SettingsPage.svelte';
  import IntegrationsPage from './components/pages/IntegrationsPage.svelte';
  import ContainerPage from './components/pages/ContainerPage.svelte';
  import StacksPage from './components/pages/StacksPage.svelte';
  import ComposePage from './components/pages/ComposePage.svelte';
  import FleetPage from './components/pages/FleetPage.svelte';
  import NavHeader from './components/layout/NavHeader.svelte';

  let initialized = $state(false);

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
    navigate('fleet', {}, true);
  }

  // Sidebar: open by default on desktop, closed on mobile (evaluated once
  // at app start so innerWidth is measured after hydration, not at import).
  // The 768px threshold must match the md: breakpoint used on the wrapper.
  $effect.pre(() => {
    if (typeof window !== 'undefined' && window.innerWidth < 768) {
      sidebarOpen.set(false);
    }
  });

  // Navheader title follows the active page (pages keep their own internal
  // sub-headings; the top-level title/description now lives only here).
  const pageTitles: Record<string, string> = {
    dashboard: 'Dashboard',
    fleet: 'Servers',
    stacks: 'Stacks',
    compose: 'Compose',
    containers: 'Containers',
    'container-detail': 'Container',
    integrations: 'Integrations',
    settings: 'Settings'
  };
  $effect(() => {
    navTitle.set(pageTitles[$currentPage] ?? 'Dashboard');
  });

  onMount(() => {
    loadUser();
    initRouter();
    // Global 401 handler: any API call with an expired/revoked token
    // dispatches this event so we drop the session back to login.
    const onUnauthorized = () => {
      currentUser.set(null);
      navigate('fleet', {}, true);
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
    <!-- Sidebar: pushes content from md (768px) up — in-flow sticky so it
         never covers the page; below md it slides in as an overlay drawer
         with a backdrop. Toggled from the hamburger in the navheader. -->
    {#if $sidebarOpen}
      <!-- Backdrop (mobile only; desktop content stays behind the sidebar) -->
      <button
        class="fixed inset-0 z-40 bg-black/60 md:hidden"
        aria-label="Close sidebar"
        onclick={() => sidebarOpen.set(false)}
      ></button>
      <div
        class="fixed inset-y-0 left-0 z-50 w-64 md:sticky md:top-0 md:bottom-auto md:z-0"
      >
        <Sidebar currentRoute={$currentPage} />
      </div>
    {/if}

    <div class="flex-1 flex flex-col min-w-0 min-h-screen">
      <NavHeader logout={logout} />
      <main class="flex-1 p-4 sm:p-6 overflow-auto">
        <!-- Remount the current page when the selected instance changes so
             instance-scoped pages (Dashboard, Stacks, Compose...) re-fetch
             instead of showing stale data from the previous server. -->
        {#key $selectedInstance}
          {#if $currentPage === 'dashboard'}
            <Dashboard />
          {:else if $currentPage === 'fleet'}
            <FleetPage />
          {:else if $currentPage === 'containers'}
            <ContainersPage />
          {:else if $currentPage === 'settings'}
            <SettingsPage />
          {:else if $currentPage === 'integrations'}
            <IntegrationsPage />
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
  </div>
{/if}

<ToastContainer />
