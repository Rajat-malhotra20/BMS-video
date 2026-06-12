import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      '/api': 'http://localhost:4000',
      '/whep': 'http://localhost:4000',
      '/live': 'http://localhost:4000',
      '/playback': 'http://localhost:4000',
    },
  },
  test: {
    environment: 'node',
  },
})
