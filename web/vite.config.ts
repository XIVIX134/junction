import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { mockApi } from "./mock/plugin.ts";

// `npm run dev`       proxies /api to the real gateway.
// `npm run dev:mock`  serves sample fixtures shaped like API.md (and marks them as sample in the UI).
export default defineConfig(({ mode }) => {
  const mock = mode === "mock";
  const proxy = mock
    ? undefined
    : { "/api": { target: "http://192.168.1.4", changeOrigin: true } };
  return {
    plugins: [react(), ...(mock ? [mockApi()] : [])],
    server: { proxy },
    preview: { proxy },
    build: { outDir: "dist", emptyOutDir: true },
  };
});
