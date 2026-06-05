import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Static marketing site. Pre-built to `dist/` and dropped into /var/www on the
// host. `base: './'` keeps asset URLs relative so the output works whether it
// is served from the domain root or opened straight from disk.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: './',
})
