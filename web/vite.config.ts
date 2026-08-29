import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig(({ mode }) => {
  if (mode === "test") {
    process.env.NODE_ENV = "test";
  }

  return {
    plugins: [react()],
    publicDir: "public",
    build: {
      outDir: "../internal/webui/dist",
      emptyOutDir: true,
    },
    test: {
      include: ["src/**/*.{test,spec}.{ts,tsx}"],
      environment: "jsdom",
      environmentOptions: {
        jsdom: {
          url: "http://localhost/",
        },
      },
      setupFiles: ["./src/test/setup.ts"],
    },
  };
});
