import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 构建产物直接输出到 Go embed 目录，dev 时把 /api 与 /api/stream 代理到后端。
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/server/web',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': { target: 'http://localhost:4939', changeOrigin: true },
      '/img': { target: 'http://localhost:4939', changeOrigin: true },
    },
  },
})
