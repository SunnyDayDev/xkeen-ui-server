import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  base: '/ui/',
  plugins: [svelte()],
  build: { outDir: '../internal/server/webdist', emptyOutDir: true }
});
