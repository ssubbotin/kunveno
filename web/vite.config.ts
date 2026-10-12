import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte()],
  server: {
    proxy: {
      '/ws': { target: 'ws://127.0.0.1:8080', ws: true },
      '/api': { target: 'http://127.0.0.1:8080' },
    },
  },
  // No source maps in builds: they embed the client sources, and the bundle ships to the server.
  build: { sourcemap: false },
  test: { environment: 'node', include: ['src/**/*.test.ts'] },
});
