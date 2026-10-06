import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: false,
      },
    },
  },
  build: {
    outDir: "dist",
    sourcemap: false,
    // Keep @license headers (React, React Router, xterm). Their MIT licences
    // require the notice in copies, and Rolldown's minifier drops it by default.
    rolldownOptions: { output: { comments: { legal: true } } },
  },
});
