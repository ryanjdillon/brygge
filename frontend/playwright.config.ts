import { randomInt } from 'node:crypto'
import { defineConfig } from '@playwright/test'

// Set once in the runner process; workers inherit it. e2e/support/stack.ts
// offsets its per-test client IPs by it so back-to-back runs do not reuse a
// rate-limit budget.
process.env.E2E_RUN_SALT ??= String(randomInt(1 << 17))

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  retries: 0,
  use: {
    baseURL: process.env.BASE_URL || 'http://localhost:5173',
    trace: 'on-first-retry',
    // Hosts where Playwright's downloaded browsers do not run (NixOS) can
    // point at an installed Chrome/Chromium instead.
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM_PATH
      ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH }
      : {},
  },
  webServer: process.env.CI
    ? undefined
    : {
        command: 'npm run dev',
        port: 5173,
        reuseExistingServer: true,
      },
})
