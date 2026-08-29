import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { defineConfig, devices } from "@playwright/test";

const configuredChromium = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE;
const systemChromium = [
  "/usr/bin/google-chrome",
  "/usr/bin/chromium",
  "/usr/bin/chromium-browser",
].find((candidate) => existsSync(candidate));
const chromiumExecutable = configuredChromium || systemChromium;

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 90_000,
  expect: { timeout: 10_000 },
  reporter: "line",
  outputDir: resolve(import.meta.dirname, "../output/playwright/test-results"),
  use: {
    ...devices["Desktop Chrome"],
    ignoreHTTPSErrors: true,
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
    video: "off",
    launchOptions: chromiumExecutable
      ? { executablePath: chromiumExecutable }
      : undefined,
  },
});
