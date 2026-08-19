<script lang="ts">
  import { api, setToken } from '../lib/api/client';
  import { currentUser, addToast } from '../lib/store';
  import type { User } from '../lib/types/api';

  let username = '';
  let password = '';
  let loading = false;
  let error = '';

  async function handleLogin() {
    if (!username || !password) {
      error = 'Username and password are required';
      return;
    }
    loading = true;
    error = '';
    try {
      const resp = await api.post<{ token: string }>('/login', { username, password });
      setToken(resp.token);
      const me = await api.get<User>('/profile');
      currentUser.set(me);
      addToast('Login successful', 'success');
    } catch (err) {
      error = err instanceof Error ? err.message : 'Login failed';
    } finally {
      loading = false;
    }
  }
</script>

<div class="min-h-screen flex items-center justify-center p-4">
  <div class="w-full max-w-md">
    <div class="text-center mb-8">
      <h1 class="text-3xl font-bold text-white mb-2">🐳 Dockpal</h1>
      <p class="text-zinc-400 text-sm">Self-hosted Docker management panel</p>
    </div>

    <div class="bg-zinc-900 border border-zinc-800 rounded-sm p-8">
      <h2 class="text-lg font-semibold text-white mb-6">Sign In</h2>

      {#if error}
        <div class="mb-4 p-3 bg-red-500/10 border border-red-500/20 rounded-sm">
          <p class="text-sm text-red-400">{error}</p>
        </div>
      {/if}

      <form onsubmit={(e) => { e.preventDefault(); handleLogin(); }} class="space-y-4">
        <div>
          <label for="username" class="block text-xs font-medium text-zinc-400 mb-1">Username</label>
          <input
            id="username"
            type="text"
            bind:value={username}
            autocomplete="username"
            class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
            placeholder="admin"
          />
        </div>

        <div>
          <label for="password" class="block text-xs font-medium text-zinc-400 mb-1">Password</label>
          <input
            id="password"
            type="password"
            bind:value={password}
            autocomplete="current-password"
            class="w-full px-3 py-2 bg-zinc-950 border border-zinc-800 rounded-sm text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-600"
            placeholder="••••••••"
          />
        </div>

        <button
          type="submit"
          disabled={loading}
          class="w-full py-2.5 bg-white hover:bg-zinc-200 disabled:opacity-50 text-zinc-900 rounded-sm text-sm font-medium transition-all"
        >
          {loading ? 'Signing in...' : 'Sign In'}
        </button>
      </form>
    </div>
  </div>
</div>
