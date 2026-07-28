import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The build output is embedded into the Go binary (go:embed web/dist) and served
// by the adminweb BFF. In dev, /api is proxied to the local gateway web port.
export default defineConfig({
  plugins: [react()],
  build: {
    // Emit straight into the Go package so it can be embedded (go:embed dist).
    outDir: "../internal/app/adminweb/dist",
    emptyOutDir: true,
  },
  server: {
    port: 5273,
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8404",
        changeOrigin: true,
      },
    },
  },
});
