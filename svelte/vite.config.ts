import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';
import path from 'path';
import { fileURLToPath } from 'url';

const dirname = path.dirname(fileURLToPath(import.meta.url));

// Dockpal Svelte SPA
// Dev: Vite serves on :5173, proxies /api (incl. WebSocket) to Go backend on :3012
// Build: static output to dist/, embedded into the Go binary and served at /
export default defineConfig({
  base: '/',
  plugins: [svelte(), tailwindcss()],
  resolve: {
    alias: {
      // Must stay in sync with the paths entry in tsconfig.json
      $lib: path.resolve(dirname, 'src/lib')
    }
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: false
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:3012',
        changeOrigin: true,
        ws: true // deploy log streaming: /api/instances/:id/deploy/stream/:deployId
      }
    }
  }
});
