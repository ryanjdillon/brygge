import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'path'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['src/test/setup.ts'],
    // Scope discovery to our own specs. The repo's in-tree Go module
    // cache (.go/) ships *.spec.js fixtures (e.g. grpc-gateway examples)
    // that vitest would otherwise try to run; restricting include to
    // src/ — plus an explicit .go exclude — keeps the suite to our tests.
    include: ['src/**/*.{test,spec}.{ts,js}'],
    exclude: ['e2e/**', 'node_modules/**', '.go/**'],
    coverage: {
      provider: 'v8',
    },
  },
})
