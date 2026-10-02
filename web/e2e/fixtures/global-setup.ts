// Playwright global setup: start a per-run server unless PCC_E2E_BASE_URL points
// at one already (scripts/e2e.sh starts it and exports the URL).
import type { FullConfig } from '@playwright/test';
import { startServer } from './server';

export default async function globalSetup(config: FullConfig): Promise<() => Promise<void>> {
  if (process.env.PCC_E2E_BASE_URL) return () => Promise.resolve();
  const srv = await startServer();
  process.env.PCC_E2E_BASE_URL = srv.baseURL;
  for (const p of config.projects) p.use.baseURL = srv.baseURL;
  return () => srv.stop();
}
