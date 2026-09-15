import { defineConfig } from '@playwright/test'
export default defineConfig({
  testDir: './tests/e2e',
  fullyParallel: false,
  workers: 1,
  timeout: 45_000,
  use: {
    baseURL: 'http://127.0.0.1:28080',
    channel: process.env.PLAYWRIGHT_CHANNEL || (process.platform === 'win32' ? 'msedge' : undefined),
    headless: true,
    trace: 'retain-on-failure',
    viewport: { width: 1440, height: 1000 },
  },
  webServer: {
    command: 'go run ./cmd/unisub',
    url: 'http://127.0.0.1:28080/login',
    reuseExistingServer: false,
    timeout: 60_000,
    env: { DATABASE_URL: 'sqlite::memory:', LISTEN_ADDR: '127.0.0.1:28080', UNISUB_MODE: 'PRD', ADMIN_USERNAME: 'e2e-admin', ADMIN_PASSWORD: 'e2e-admin-password' },
  },
})
