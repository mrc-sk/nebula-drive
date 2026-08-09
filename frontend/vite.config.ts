import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { fileURLToPath, URL } from 'node:url'

// 单二进制内嵌：build 输出到 ../backend/frontend_dist，由 Go go:embed
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:5212',
      '/dav': 'http://127.0.0.1:5212',
    },
  },
  build: {
    outDir: fileURLToPath(new URL('../backend/frontend_dist', import.meta.url)),
    emptyOutDir: true,
  },
})
