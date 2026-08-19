import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// Dockpal Svelte SPA
// Dev: Vite serves on :5173, proxies /api + /ws to Go backend on :3012
// Build: static output to dist/, later embedded into Go binary (Phase 5)
export default defineConfig({
  plugins: [svelte()],
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
        changeOrigin: true
      },
      '/ws': {
        target: 'ws://localhost:3012',
        ws: true
      }
    }
  }
});
