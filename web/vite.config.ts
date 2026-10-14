import { defineConfig } from 'vitest/config';
import type { Plugin } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// Library worklet scripts are copied as they are and end with a sourceMappingURL line; the bundle
// ships no source maps, and the image build rejects the line, so it is cut from emitted assets.
function stripSourceMapComments(): Plugin {
  return {
    name: 'strip-source-map-comments',
    apply: 'build',
    generateBundle(_, bundle) {
      for (const f of Object.values(bundle)) {
        if (f.type !== 'asset' || !f.fileName.endsWith('.js')) continue;
        const text = typeof f.source === 'string' ? f.source : new TextDecoder().decode(f.source);
        f.source = text.replace(/\n?\/\/# sourceMappingURL=\S*\s*$/, '\n');
      }
    },
  };
}

export default defineConfig({
  plugins: [svelte(), stripSourceMapComments()],
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
