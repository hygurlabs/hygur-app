import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { resolve } from 'node:path'

// The build output lands directly inside the Go package that go:embed's it,
// so `make` only needs to run `vite build` before compiling the sidecar.
// `base: './'` keeps asset URLs relative — robust whether the SPA is served
// from `/` (sidecar) or opened from disk.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: './',
  build: {
    outDir: resolve(import.meta.dirname, '../sidecar/internal/api/webui/dist'),
    emptyOutDir: true,
  },
})
