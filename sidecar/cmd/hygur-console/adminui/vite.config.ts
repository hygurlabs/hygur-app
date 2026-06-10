import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Operator admin SPA, embedded into the hygur-console binary (go:embed dist) and
// served under /admin. base: "./" keeps asset URLs relative so it works behind
// the /admin path prefix.
export default defineConfig({
  plugins: [react()],
  base: "/admin/",
});
