# Phase 3: Authentication & Routing

## Overview
Implement authentication flow using Svelte stores and router integration. Replace Alpine.js auth state management with typed store patterns.

## Timeline
**Duration:** 3-4 days
**Priority:** High

---

## Task List

### 3.1 Router Configuration

#### Create `src/app.route.ts`

```typescript
import type { RequestHandler } from '@sveltejs/kit';

// Auth middleware simulation (client-side guard)
export const beforeRender: ImportMetaEnv['beforeRender'] = async ({ data, form }) => {
  const token = localStorage.getItem('dockpal_token');
  
  if (!token && !data?.public) {
    return { redirect: '/login' };
  }
};
```

#### Create routing file structure

```
src/routes/
├── +layout.svelte          # Main application shell
├── +page.svelte            # Dashboard page
├── login/+page.svelte      # Login page
├── containers/
│   ├── +page.svelte        # Container list
│   └── [id]/
│       ├── +page.svelte    # Container detail
│       ├── logs/+page.svelte
│       └── exec/+page.svelte
├── images/
│   └── +page.svelte        # Image registry
├── deploy/
│   ├── +page.svelte        # Deploy wizard
│   └── template/[id]/
│       └── +page.svelte    # Template-specific deployment
├── settings/
│   ├── +page.svelte        # General settings
│   ├── users/+page.svelte  # User management
│   └── registry/+page.svelte
└── api/                    # API routes (server actions)
    └── +server.ts
```

---

### 3.2 Login Flow

#### Create `src/routes/login/+page.svelte`

```svelte
<script lang="ts">
  import { goto } from '$app/navigation';
  import Button from '$lib/components/ui/Button.svelte';
  import Input from '$lib/components/Form/Input.svelte';
  import { user } from '$lib/store';
  
  let username = '';
  let password = '';
  let loading = false;
  let error = '';

  async function handleLogin(event: Event) {
    event.preventDefault();
    
    if (!username || !password) {
      error = 'Username and password are required';
      return;
    }

    loading = true;
    error = '';

    try {
      const response = await fetch('/api/auth', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, password })
      });

      if (!response.ok) {
        throw new Error('Invalid credentials');
      }

      const data = await response.json();
      localStorage.setItem('dockpal_token', data.token);
      
      // Fetch current user profile
      const userProfile = await fetch('/api/auth/me', {
        headers: { Authorization: `Bearer ${data.token}` }
      }).then(r => r.json());

      user.set(userProfile);
      goto('/');
    } catch (err) {
      error = err instanceof Error ? err.message : 'Login failed';
    } finally {
      loading = false;
    }
  }
</script>

<div class="min-h-screen bg-zinc-950 flex items-center justify-center p-4">
  <div class="w-full max-w-md">
    <!-- Header -->
    <div class="text-center mb-8">
      <h1 class="text-3xl font-bold text-white mb-2">Dockpal</h1>
      <p class="text-zinc-400">Self-hosted Docker management panel</p>
    </div>

    <!-- Card -->
    <div class="bg-zinc-900 border border-zinc-800 rounded-sm p-8">
      <h2 class="text-xl font-semibold text-white mb-6">Sign In</h2>

      {#if error}
        <div class="mb-6 p-4 bg-red-500/10 border border-red-500/20 rounded-sm">
          <p class="text-sm text-red-400">{error}</p>
        </div>
      {/if}

      <form onsubmit={handleLogin} class="space-y-4">
        <Input 
          name="username"
          label="Username"
          placeholder="admin"
          bind:value
        />

        <Input 
          name="password"
          type="password"
          label="Password"
          placeholder="••••••••"
          bind:value
        />

        <Button 
          variant="primary" 
          size="lg"
          loading={loading}
          type="submit"
          class="w-full">
          Sign In
        </Button>
      </form>

      <div class="mt-6 text-xs text-zinc-600 text-center">
        First login uses admin credentials set during initial installation
      </div>
    </div>
  </div>
</div>
```

---

### 3.3 Auth Store Integration

#### Extend `$lib/store.ts` with authentication methods

```typescript
import { writable, derived } from 'svelte/store';

// Authentication stores
export const isAuthenticated = derived(user, $user => !!$user);

export const hasRole = (role: string) => 
  derived(user, $user => {
    if (!$user) return false;
    const roles = ['admin', 'operator', 'viewer'];
    return roles.indexOf($user.role) >= roles.indexOf(role);
  });

export const canManageUsers = hasRole('admin');
export const canDeploy = hasRole('operator') || hasRole('admin');

// Logout action
export async function logout() {
  try {
    const token = localStorage.getItem('dockpal_token');
    if (token) {
      await fetch('/api/logout', {
        method: 'POST',
        headers: { Authorization: `Bearer ${token}` }
      });
    }
  } finally {
    localStorage.removeItem('dockpal_token');
    user.set(null);
    goto('/login');
  }
}
```

---

### 3.4 Protected Route Guards

#### Create `src/lib/middleware/guard.svelte.ts`

```typescript
import { browser } from '$app/environment';
import { goto } from '$app/navigation';
import { user, isAuthenticated } from '../store';

export function requireAuth() {
  if (!browser) return;

  const token = localStorage.getItem('dockpal_token');
  if (!token) {
    goto('/login');
    return;
  }

  // Verify token validity
  fetch('/api/auth/me', {
    headers: { Authorization: `Bearer ${token}` }
  })
    .then(r => {
      if (!r.ok) {
        localStorage.removeItem('dockpal_token');
        goto('/login');
        return;
      }
      return r.json();
    })
    .then(profile => {
      user.set(profile);
    })
    .catch(() => {
      localStorage.removeItem('dockpal_token');
      goto('/login');
    });
}

export function requireRole(...allowedRoles: string[]) {
  return () => {
    if (!browser) return;
    
    requireAuth();
    
    const currentUser = user;
    if (currentUser && !allowedRoles.includes(currentUser.role)) {
      goto('/'); // Redirect unauthorized users
    }
  };
}
```

---

### 3.5 Layout Component Updates

#### Update `src/routes/+layout.svelte`

```svelte
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { goto, url } from '$app/navigation';
  import { user, user as userStore } from '$lib/store';
  import { requireAuth } from '$lib/middleware/guard';
  import Sidebar from '$lib/components/layout/Sidebar.svelte';
  import Header from '$lib/components/layout/Header.svelte';
  import ToastContainer from '$lib/components/layout/ToastContainer.svelte';

  let currentUrl = $url.pathname;
  let showSidebar = true;

  onMount(() => {
    requireAuth();
    
    // Load initial data after auth
    loadInitialData();
  });

  async function loadInitialData() {
    try {
      const token = localStorage.getItem('dockpal_token');
      if (token) {
        const profile = await fetch('/api/auth/me', {
          headers: { Authorization: `Bearer ${token}` }
        }).then(r => r.json());
        
        user.set(profile);
      }
    } catch (error) {
      console.error('Failed to load user:', error);
    }
  }
</script>

<svelte:head>
  <title>Dockpal - Docker Management</title>
</svelte:head>

{#if showSidebar}
  <div class="flex min-h-screen bg-zinc-950">
    <Sidebar />
    
    <div class="flex-1 flex flex-col">
      <Header />
      
      <main class="flex-1 overflow-auto p-6">
        {@render children()}
      </main>
      
      <ToastContainer />
    </div>
  </div>
{:else}
  {@render children()}
{/if}
```

---

### 3.6 Navigation & Links

#### Create `src/lib/components/layout/Sidebar.svelte`

```svelte
<script lang="ts">
  import { goto, url } from '$app/navigation';
  import { user, logout } from '$lib/store';
  import { hasRole } from '$lib/store';
  
  import Button from '../ui/Button.svelte';
  import IconMenu from './IconMenu.svelte';

  export let role = 'operator';

  const navigation = [
    { href: '/', label: 'Dashboard', icon: '📊', roles: ['admin', 'operator', 'viewer'] },
    { href: '/containers', label: 'Containers', icon: '🐳', roles: ['admin', 'operator', 'viewer'] },
    { href: '/images', label: 'Images', icon: '📦', roles: ['admin', 'operator', 'viewer'] },
    { href: '/deploy', label: 'Deploy', icon: '🚀', roles: ['admin', 'operator'] },
    { href: '/settings', label: 'Settings', icon: '⚙️', roles: ['admin', 'operator'] }
  ];

  function navigate(href: string) {
    goto(href);
  }

  function handleLogout() {
    logout();
  }
</script>

<aside class="w-64 border-r border-zinc-800 bg-zinc-900 p-4 flex flex-col h-screen fixed left-0 top-0 z-10">
  <!-- Logo -->
  <div class="mb-8 px-2">
    <h1 class="text-xl font-bold text-white tracking-tight">Dockpal</h1>
    <p class="text-xs text-zinc-500 mt-1">Docker Management</p>
  </div>

  <!-- Navigation -->
  <nav class="flex-1 space-y-1">
    {#each navigation as item}
      {#if item.roles.includes($user.role)}
        <button
          class="w-full flex items-center gap-3 px-3 py-2 rounded-sm text-sm transition-colors hover:bg-zinc-800 group"
          on:click={() => navigate(item.href)}>
          <span class="text-lg">{item.icon}</span>
          <span class="text-zinc-300 group-hover:text-white transition-colors">{item.label}</span>
        </button>
      {/if}
    {/each}
  </nav>

  <!-- Footer -->
  <div class="border-t border-zinc-800 pt-4 mt-4">
    {#if $user}
      <div class="px-3 mb-3">
        <div class="text-sm font-medium text-white">{$user.username}</div>
        <div class="text-xs text-zinc-500 capitalize">{$user.role}</div>
      </div>
      
      <Button 
        variant="secondary"
        size="sm"
        on:click={handleLogout}>
        <svg class="w-4 h-4 mr-2" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1"/>
        </svg>
        Logout
      </Button>
    {:else}
      <Button 
        variant="primary"
        size="sm"
        on:click={() => goto('/login')}
        class="w-full">
        Login
      </Button>
    {/if}
  </div>
</aside>

<style>
  aside { transition: transform 0.3s ease; }
</style>
```

---

### 3.7 Toast Notification System

#### Create `src/lib/components/layout/ToastContainer.svelte`

```svelte
<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import Button from '../ui/Button.svelte';

  interface Toast {
    id: string;
    message: string;
    type: 'success' | 'error' | 'warning' | 'info';
    duration?: number;
  }

  let toasts: Toast[] = [];
  const dispatch = createEventDispatcher();

  function addToast(message: string, type: Toast['type'] = 'info', duration = 5000) {
    const id = Math.random().toString(36).substr(2, 9);
    toasts = [...toasts, { id, message, type, duration }];

    setTimeout(() => {
      removeToast(id);
    }, duration);
  }

  function removeToast(id: string) {
    toasts = toasts.filter(t => t.id !== id);
  }

  function getTypeClasses(type: string): string {
    switch(type) {
      case 'success': return 'bg-emerald-500 text-white border-emerald-600';
      case 'error': return 'bg-red-500 text-white border-red-600';
      case 'warning': return 'bg-amber-500 text-white border-amber-600';
      default: return 'bg-blue-500 text-white border-blue-600';
    }
  }

  // Listen for custom events from components
  window.addEventListener('toast-add', (e: CustomEvent) => {
    addToast(e.detail.message, e.detail.type, e.detail.duration);
  });
</script>

<!-- Dispatch toast listener -->
<svelte:window on:toastAdd={(e) => addToast(e.detail.message, e.detail.type, e.detail.duration)} />

<div class="fixed bottom-4 right-4 z-50 space-y-2">
  {#each toasts as toast}
    <div 
      class="px-4 py-3 rounded-sm shadow-lg border animate-in slide-in-from-right-10 fade-in duration-200 {getTypeClasses(toast.type)}">
      <div class="flex items-center gap-3">
        <span class="text-sm font-medium">{toast.message}</span>
        <button 
          on:click={() => removeToast(toast.id)}
          class="text-white/80 hover:text-white">
          ×
        </button>
      </div>
    </div>
  {/each}
</div>
```

#### Export toast utility functions

```typescript
// src/lib/components/utils/toast.ts

export function showToast(message: string, type: 'success' | 'error' | 'warning' | 'info' = 'info', duration = 5000) {
  window.dispatchEvent(new CustomEvent('toast-add', {
    detail: { message, type, duration }
  }));
}
```

---

## Deliverables Checklist

- [ ] ✅ Login page with form validation
- [ ] ✅ Auth store with derived state (isAuthenticated, hasRole)
- [ ] ✅ Protected route guards (requireAuth, requireRole)
- [ ] ✅ Updated layout with authentication check
- [ ] ✅ Sidebar navigation component with role-based rendering
- [ ] ✅ Toast notification system
- [ ] ✅ Logout functionality
- [ ] ✅ Token refresh mechanism

---

**Next step:** Proceed to **Phase 4: Data Integration & Real-time Updates** once authentication is complete.
