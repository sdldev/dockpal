# Phase 1: SvelteKit Setup & Scaffolding

## Overview
Initialize SvelteKit monorepo structure alongside existing Go backend. Establish build pipeline for production embedding.

## Timeline
**Duration:** 2-3 days (including testing)
**Priority:** Critical path dependency

---

## Task List

### 1.1 SvelteKit Project Initialization

#### Create `svelte/` directory structure

```bash
cd /home/indatech/Documents/2026/dev/dockpal-project/dockpal
mkdir -p svelte/src/{lib,routes,components,pages}
mkdir -p svelte/tests/e2e
```

#### Initialize package.json

```json
{
  "name": "dockpal-svelte",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite dev --host",
    "build": "vite build && node scripts/copy-to-web.js",
    "preview": "vite preview",
    "lint": "svelte-check --tsconfig ./tsconfig.json",
    "test:e2e": "playwright test",
    "test:unit": "vitest run"
  },
  "devDependencies": {
    "@sveltejs/adapter-auto": "^4.0.0",
    "@sveltejs/kit": "^2.20.0",
    "@sveltejs/vite-plugin-svelte": "^5.0.0",
    "@types/node": "^22.0.0",
    "typescript": "^5.7.0",
    "vite": "^6.0.0",
    "vitest": "^3.0.0",
    "svelte-check": "^4.0.0"
  },
  "dependencies": {
    "alpinejs": "^3.14.0",
    "chart.js": "^4.4.0",
    "chartjs-adapter-date-fns": "^3.0.0"
  }
}
```

#### Create vite.config.ts

```typescript
import { defineConfig } from 'vite';
import { sveltekit } from '@sveltejs/kit/vite';
import path from 'path';

export default defineConfig({
  plugins: [sveltekit()],
  build: {
    outDir: '../web',
    rollupOptions: {
      output: {
        manualChunks: {
          vendor: ['alpinejs', 'chart.js'],
          utils: ['chartjs-adapter-date-fns']
        }
      }
    }
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:3012',
      '/assets': {
        target: 'http://localhost:3012',
        rewrite: (path) => path.replace('/assets', '')
      }
    }
  }
});
```

#### Create `package-lock.json` and lock dependencies

```bash
cd svelte
npm install
```

---

### 1.2 TypeScript Configuration

#### tsconfig.json (root level)

```json
{
  "extends": "./.svelte-kit/tsconfig.json",
  "compilerOptions": {
    "moduleResolution": "bundler",
    "module": "esnext",
    "target": "esnext",
    "lib": ["dom", "esnext"],
    "resolveJsonModule": true,
    "allowSyntheticDefaultImports": true,
    "strict": true,
    "skipLibCheck": true,
    "forceConsistentCasingInFileNames": true,
    "baseUrl": ".",
    "paths": {
      "$lib": ["src/lib/*"],
      "$lib/*": ["src/lib/*"]
    }
  },
  "include": ["src/**/*.ts", "src/**/*.svelte", "tests/**/*.ts"]
}
```

---

### 1.3 API Client Library

#### Create `src/lib/api/client.ts`

```typescript
/**
 * Unified API client for Dockpal backend.
 * Handles auth headers, error formatting, retry logic.
 */

const API_BASE = '/api';

interface RequestOptions extends RequestInit {
  retry?: number;
  parseResponse?: boolean;
}

async function request<T>(
  endpoint: string,
  options: RequestOptions = {}
): Promise<T> {
  const { retry = 1, parseResponse = true, ...fetchOptions } = options;

  const doRequest = async (): Promise<T> => {
    const headers = new Headers(fetchOptions.headers);
    const token = localStorage.getItem('dockpal_token');
    
    if (token) {
      headers.set('Authorization', `Bearer ${token}`);
    }

    fetchOptions.headers = headers;

    const url = `${API_BASE}${endpoint.startsWith('/') ? endpoint : '/' + endpoint}`;
    const response = await fetch(url, fetchOptions);

    if (!response.ok) {
      let errorMessage = `HTTP ${response.status}`;
      
      try {
        const errorBody = await response.json();
        errorMessage = errorBody.error || errorMessage;
      } catch {
        // Ignore JSON parse errors
      }

      throw new Error(errorMessage);
    }

    return parseResponse ? response.json() : null;
  };

  try {
    return await doRequest();
  } catch (error) {
    if (retry > 0) {
      console.warn(`Retrying ${endpoint} after failure...`);
      return await doRequest();
    }
    throw error;
  }
}

// HTTP method helpers
export const api = {
  get<T>(endpoint: string): Promise<T> {
    return request<T>(endpoint, { method: 'GET' });
  },

  post<T>(endpoint: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    return request<T>(endpoint, { 
      method: 'POST',
      body: JSON.stringify(body),
      headers
    });
  },

  put<T>(endpoint: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    return request<T>(endpoint, { 
      method: 'PUT',
      body: JSON.stringify(body),
      headers
    });
  },

  delete<T>(endpoint: string): Promise<T> {
    return request<T>(endpoint, { method: 'DELETE' });
  },

  stream(endpoint: string, onChunk: (data: unknown) => void): () => void {
    const token = localStorage.getItem('dockpal_token');
    const wsUrl = `ws://localhost:3012/ws/stream?token=${token}`;
    const ws = new WebSocket(wsUrl);

    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        onChunk(data);
      } catch {
        // Ignore parse errors
      }
    };

    return () => ws.close();
  }
};
```

#### Create `src/lib/types/api.ts`

```typescript
// Type definitions for API responses

export interface Service {
  id: string;
  name: string;
  status: 'running' | 'stopped' | 'degraded' | 'error';
  type: 'container' | 'compose' | 'git' | 'template';
  instance_id?: string;
  ports: Array<{ label: string; host_port: number; container_port: number }>;
  environment: Record<string, string>;
  domains?: string[];
  created_at: Date;
  updated_at: Date;
}

export interface DeployLog {
  time: string;
  step: 'start' | 'pull' | 'create' | 'deploy' | 'done' | 'hint';
  message: string;
  status?: 'pending' | 'error' | 'done';
}

export interface Template {
  id: string;
  name: string;
  description: string;
  category: string;
  icon: string;
  env_required: string[];
  ports: Array<{ label: string; default: number; container_port: number }>;
  compose: string;
}

export interface User {
  id: string;
  username: string;
  role: 'admin' | 'operator' | 'viewer';
  created_at: Date;
}

export interface HealthStatus {
  status: 'healthy' | 'unhealthy';
  components: {
    database: 'ok' | 'error';
    docker: 'ok' | 'error';
    agent?: 'ok' | 'error' | 'offline';
  };
  version: string;
  uptime_seconds: number;
}
```

---

### 1.4 Layout Skeleton

#### Create `src/routes/+layout.svelte`

```svelte
<script lang="ts">
  import '../app.css';
  import { onMount } from 'svelte';
  
  export let children;
  
  let user: User | null = null;
  let loading = true;

  onMount(async () => {
    try {
      const me = await api.get<User>('/auth/me');
      user = me;
    } catch {
      user = null;
    } finally {
      loading = false;
    }
  });
</script>

<svelte:head>
  <title>Dockpal</title>
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <link rel="stylesheet" href="/assets/alpine.min.css" />
  <script defer src="/assets/alpine.min.js"></script>
</svelte:head>

<div class="min-h-screen bg-zinc-950 text-white flex">
  <!-- Sidebar -->
  <aside class="w-64 border-r border-zinc-800 bg-zinc-900 p-4 space-y-4">
    <div class="text-lg font-bold text-white mb-6">Dockpal</div>
    
    <nav class="space-y-2">
      <button class="w-full text-left px-3 py-2 rounded-sm hover:bg-zinc-800 transition-colors">
        Dashboard
      </button>
      <button class="w-full text-left px-3 py-2 rounded-sm hover:bg-zinc-800 transition-colors">
        Containers
      </button>
      <button class="w-full text-left px-3 py-2 rounded-sm hover:bg-zinc-800 transition-colors">
        Images
      </button>
      <button class="w-full text-left px-3 py-2 rounded-sm hover:bg-zinc-800 transition-colors">
        Deploy
      </button>
    </nav>

    {#if user}
      <div class="pt-4 border-t border-zinc-800 text-xs text-zinc-500">
        Logged in as {user.username} ({user.role})
      </div>
    {:else}
      <a href="/login" class="block text-center px-3 py-2 rounded-sm bg-white text-zinc-900 hover:bg-zinc-200 transition-colors mt-4">
        Login
      </a>
    {/if}
  </aside>

  <!-- Main content -->
  <main class="flex-1 overflow-auto">
    {@render children()}
  </main>
</div>
```

#### Create `src/app.css`

```css
@tailwind base;
@tailwind components;
@tailwind utilities;

/* Custom Tailwind config via postcss */
body {
  @apply antialiased font-sans;
}

/* Alpine.js overrides */
[x-cloak] { display: none !important; }
```

---

### 1.5 Production Build Integration

#### Create `scripts/copy-to-web.js`

```javascript
// script to copy Vite build output to dockpal/web directory
const fs = require('fs');
const path = require('path');

const src = path.resolve(__dirname, '..', 'dist');
const dest = path.resolve(__dirname, '..', '..', 'web');

console.log(`Copying from ${src} to ${dest}...`);

// Ensure destination exists
if (!fs.existsSync(dest)) {
  fs.mkdirSync(dest, { recursive: true });
}

// Copy dist contents
fs.readdirSync(src).forEach(file => {
  const srcPath = path.join(src, file);
  const destPath = path.join(dest, file);

  if (fs.statSync(srcPath).isDirectory()) {
    if (!fs.existsSync(destPath)) {
      fs.mkdirSync(destPath, { recursive: true });
    }
    fs.readdirSync(srcPath).forEach(subFile => {
      fs.copyFileSync(path.join(srcPath, subFile), path.join(destPath, subFile));
    });
  } else {
    fs.copyFileSync(srcPath, destPath);
  }
});

console.log('Copy complete!');
```

#### Update `go.mod` embed directive

Modify `web/embed.go` comment:

```go
//go:embed index.html assets pages partials templates
var Assets embed.FS
```

Add template folder reference:

```bash
cp -r templates/ svelte/dist/templates/ 2>/dev/null || true
```

---

### 1.6 Verification Checklist

- [ ] `npm run dev` starts local Svelte app on port 5173
- [ ] `/api/auth/me` returns current user from Go backend
- [ ] Cross-origin requests work (Vite proxy configured)
- [ ] TypeScript compiles without errors (`npm run lint`)
- [ ] `npm run build` creates `../web/` directory with assets
- [ ] Go binary embeds new frontend (`make build` succeeds)
- [ ] Running `./dockpal server` serves Svelte app correctly

---

## Deliverables

1. ✅ SvelteKit project structure at `svelte/`
2. ✅ TypeScript + ESLint configuration
3. ✅ Unified API client library (`src/lib/api/`)
4. ✅ Layout skeleton with sidebar navigation
5. ✅ Build pipeline (dev → prod) integrated
6. ✅ Production build output to `web/` directory

## Dependencies for Next Phases

- ✅ Phase 2: Components (Dashboard, Services table)
- ✅ Phase 3: Auth flow implementation
- ✅ Phase 4: Deployment wizard

## Notes

- Keep Alpine.js installed for backward compatibility during migration
- Use TypeScript strict mode from start (`strict: true` in tsconfig)
- Document all API endpoints in OpenAPI spec for TypeScript generation
- Store `.env.local` in `.gitignore` for secrets management

---

**Next step:** Proceed to **Phase 2: Component Migration** once this verification checklist is complete.
