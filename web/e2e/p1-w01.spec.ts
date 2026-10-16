// P1-W01 end to end: the co-sign dialog with Chromium's virtual authenticator,
// through the real binary. A genesis account enrolls a passkey, opens a
// co-sign intent (the test-only cosign_check purpose), sees the exact record
// and signs it; an account without a passkey cannot open one. Selectors avoid
// English text so the spec runs in en and in the pseudo-locale en-XA.
import { expect, request as pwRequest, test, type Page } from '@playwright/test';
import { addVirtualAuthenticator } from './fixtures/auth';
import { World } from './fixtures/world';

let origin = '';

test.beforeEach(({ baseURL }) => {
  origin = baseURL ?? '';
});

async function world(): Promise<World> {
  const ctx = await pwRequest.newContext({ baseURL: origin });
  return new World(ctx, origin);
}

async function enroll(page: Page, w: World, handle: string): Promise<void> {
  const token = await w.genesisToken(handle);
  await page.goto(`${origin}/enroll#${token}`);
  await page.locator('input[name=tos]').check();
  await page.locator('input[name=age]').check();
  await page.getByTestId('enroll-create').click();
  await page.getByTestId('second-done').click();
  await expect(page).toHaveURL(`${origin}/`);
}

test('the co-sign dialog shows the exact record and signs it with the passkey', async ({ page, context }) => {
  const w = await world();
  await w.reset();
  await addVirtualAuthenticator(context, page);
  await enroll(page, w, 'alice');

  await page.goto(`${origin}/cosign/check`);
  const begun = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/cosign');
  await page.getByTestId('cosign-check-run').click();
  const res = await begun;
  expect(res.status()).toBe(200);
  const intent = (await res.json()) as { intent: string; record: Record<string, unknown>; options: unknown };
  const dialog = page.getByTestId('cosign-dialog');
  await expect(dialog).toBeVisible();
  const record = dialog.getByTestId('cosign-record');
  await expect(record).toHaveAttribute('data-kind', 'passkey_change');
  // The exact JSON shown is the server's draft, nonce and issue time included.
  const exact = await dialog.getByTestId('cosign-exact').textContent();
  expect(JSON.parse(exact ?? '{}')).toEqual(intent.record);
  expect((intent.record.cosign as { nonce?: string }).nonce).toMatch(/^[0-9a-f-]{36}$/);

  await dialog.getByTestId('cosign-sign').click();
  await expect(page.getByTestId('cosign-check-signed')).toBeVisible();
  await expect(dialog).toHaveCount(0);

  // Cancelling resolves with nothing signed.
  await page.getByTestId('cosign-check-run').click();
  await expect(page.getByTestId('cosign-sign')).toBeVisible();
  await page.getByTestId('cosign-cancel').click();
  await expect(page.getByTestId('cosign-check-cancelled')).toBeVisible();
});

test('an account without a passkey cannot open an intent; unknown purposes are not found', async ({ page }) => {
  const w = await world();
  await w.reset();
  await w.seed('p1-w01');
  const csrf = await new World(page.request, origin).login('bob');
  const headers = { Origin: origin, 'Sec-Fetch-Site': 'same-origin', 'X-PCC-CSRF': csrf };
  for (const purpose of ['cosign_check', 'friend_request', 'nope']) {
    const res = await page.request.post(`${origin}/api/cosign`, { headers, data: { purpose } });
    expect(res.status()).toBe(404);
    expect(await res.json()).toEqual({ error: 'not_found' });
  }
  await page.goto(`${origin}/cosign/check`);
  await page.getByTestId('cosign-check-run').click();
  await expect(page.getByTestId('cosign-dialog').getByRole('alert')).toBeVisible();
  await expect(page.getByTestId('cosign-sign')).toHaveCount(0);
  await page.getByTestId('cosign-cancel').click();
  await expect(page.getByTestId('cosign-check-cancelled')).toBeVisible();
});
