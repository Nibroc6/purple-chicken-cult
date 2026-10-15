// Playwright (SCAFFOLD-0b; plan §7). Chromium only. Each run gets its own pcc
// binary process on a free port, its own database cloned from the migrated
// template and its own static dir (e2e/fixtures/server.ts via globalSetup), so
// concurrent runs never interfere. Projects: en and the pseudo-locale en-XA.
import { defineConfig, devices } from '@playwright/test';

const baseURL = process.env.PCC_E2E_BASE_URL ?? 'http://127.0.0.1:0';

export default defineConfig({
  testDir: './e2e',
  testMatch: ['**/*.spec.ts'],
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  globalSetup: './e2e/fixtures/global-setup.ts',
  use: {
    baseURL,
    trace: 'retain-on-failure',
    // Fake camera/mic for the handshake (P0-W04 writes the Y4M; P1 audio via WAV).
    launchOptions: { args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream'] },
  },
  projects: [
    { name: 'en', use: { ...devices['Desktop Chrome'], locale: 'en' } },
    { name: 'en-XA', use: { ...devices['Desktop Chrome'], locale: 'en-XA' } },
  ],
});
