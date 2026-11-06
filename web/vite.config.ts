import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath, URL } from 'node:url'

// Dev: vite on 5173, /api proxied to the Go backend on 127.0.0.1:7333.
// Prod: `vite build` → web/dist, embedded into the loom binary.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:7333',
        // MUST be true. The backend's CSRF guard (internal/httpapi/csrf.go)
        // rejects a write whose Host header is not its own listen address —
        // that is its DNS-rebinding defence. Without this the proxy forwards
        // `Host: localhost:5173` and every write 403s in dev while working
        // fine under curl, which is a genuinely confusing failure to debug.
        // Origin stays `http://localhost:5173`, which the guard allowlists.
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: false,
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
    setupFiles: ['./src/test/setup.ts'],
    restoreMocks: true,
  },
})
