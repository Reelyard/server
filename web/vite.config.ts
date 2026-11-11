import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// `npm run dev` serves the UI on :5173 and forwards /api to a Harbor
// server on :8787 (start one with `go run ./cmd/harbor serve`).
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: { outDir: "dist", emptyOutDir: true, sourcemap: false },
  server: { proxy: { "/api": "http://localhost:8787" } },
});
