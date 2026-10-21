// P0-W11 end to end: settings pages, export with a fresh step-up, account
// deletion and the moderator recovery page, through the real binary. Selectors
// avoid English text so the spec runs in en and in the pseudo-locale en-XA.
import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { World } from './fixtures/world';

// signIn resets the run, creates a genesis account and signs the page in. The
// returned world drives the hooks from a cookie-less context (a signed-in
// context would need the CSRF token on every hook call); login() replaces the
// page's session with a fresh one (a new passkey time).
async function signIn(
  page: Page,
  request: APIRequestContext,
  baseURL: string | undefined,
  handle: string,
): Promise<{ world: World; login: () => Promise<string>; csrf: string }> {
  const origin = baseURL ?? '';
  const world = new World(request, origin);
  await world.reset();
  await world.genesisToken(handle);
  const login = async () => {
    await page.context().clearCookies();
    return new World(page.request, origin).login(handle);
  };
  const csrf = await login();
  return { world, login, csrf };
}

function apiHeaders(origin: string, csrf: string): Record<string, string> {
  return { Origin: origin, 'Sec-Fetch-Site': 'same-origin', 'X-PCC-CSRF': csrf };
}

async function me(request: APIRequestContext): Promise<{ status: number; body: Record<string, unknown> }> {
  const res = await request.get('/api/me', { headers: { 'Sec-Fetch-Site': 'same-origin' } });
  return { status: res.status(), body: res.ok() ? ((await res.json()) as Record<string, unknown>) : {} };
}

test('display settings persist and switch accessibility on every page', async ({ page, request, baseURL }) => {
  await signIn(page, request, baseURL, 'alice');
  await page.goto('/settings/display');
  await page.locator('input[type=radio][value=compact]').first().check();
  await page.locator('select').nth(1).selectOption('large');
  await page.locator('input[type=radio][value=latest]').check();
  await page.locator('form button[type=submit]').click();
  await expect(page.locator('.settings-actions [role=status]')).toBeVisible();
  await expect(page.locator('html')).toHaveAttribute('data-text-scale', 'large');

  await page.goto('/settings');
  await expect(page.locator('html')).toHaveAttribute('data-text-scale', 'large');
  const { body } = await me(page.request);
  const settings = body.settings as { rank_preset: string; ui_prefs: { format: string } };
  expect(settings.rank_preset).toBe('latest');
  expect(settings.ui_prefs.format).toBe('compact');

  await page.goto('/settings/display');
  await expect(page.locator('input[type=radio][value=compact]').first()).toBeChecked();
});

test('settings reject translate_to outside languages and an unknown preset', async ({ page, request, baseURL }) => {
  const { csrf } = await signIn(page, request, baseURL, 'alice');
  const headers = apiHeaders(baseURL ?? '', csrf);
  for (const [data, code] of [
    [{ translate_to: 'de' }, 'translate_to_not_in_languages'],
    [{ rank_preset: 'Top' }, 'preset_unavailable'],
    [{ ui_prefs: { format: 'grid' } }, 'ui_prefs_invalid'],
  ] as const) {
    const res = await page.request.patch('/api/me/settings', { data, headers });
    expect(res.status()).toBe(422);
    expect(((await res.json()) as { error: string }).error).toBe(code);
  }

  // Through the page: add a language, translate into it, save.
  await page.goto('/settings/languages');
  await page.locator('input').nth(1).fill('de');
  await page.locator('fieldset button[type=button]').last().click();
  await page.locator('select').selectOption('de');
  await page.locator('form > .settings-actions button[type=submit]').click();
  await expect(page.locator('form > .settings-actions [role=status]')).toBeVisible();
  const { body } = await me(page.request);
  const settings = body.settings as { languages: string[]; translate_to: string };
  expect(settings.languages).toEqual(['en', 'de']);
  expect(settings.translate_to).toBe('de');
});

test('privacy toggles save', async ({ page, request, baseURL }) => {
  await signIn(page, request, baseURL, 'alice');
  await page.goto('/settings/privacy');
  const boxes = page.locator('input[type=checkbox]');
  await expect(boxes).toHaveCount(4);
  await boxes.nth(0).check();
  await page.locator('form button[type=submit]').click();
  await expect(page.locator('.settings-actions [role=status]')).toBeVisible();
  const { body } = await me(page.request);
  expect((body.settings as { name_in_chains: boolean }).name_in_chains).toBe(true);
});

test('export downloads a signed zip and needs a fresh step-up', async ({ page, request, baseURL }) => {
  const { world } = await signIn(page, request, baseURL, 'alice');
  await page.goto('/settings/export');
  const download = page.waitForEvent('download');
  await page.locator('.settings-actions button').click();
  const file = await download;
  expect(file.suggestedFilename()).toMatch(/^pcc-export-\d{4}-\d{2}-\d{2}\.zip$/);
  const stream = await file.createReadStream();
  const chunks: Buffer[] = [];
  for await (const c of stream) chunks.push(c as Buffer);
  expect(Buffer.concat(chunks).subarray(0, 2).toString()).toBe('PK');

  await world.advance('6m');
  await page.reload();
  await page.locator('.settings-actions button').click();
  await expect(page.locator('.settings-error[role=alert]')).toBeVisible();
  const res = await page.request.post('/api/me/export', {
    data: {},
    headers: { Origin: baseURL ?? '', 'Sec-Fetch-Site': 'same-origin' },
  });
  expect(res.status()).toBe(404); // no CSRF header: the uniform 404
});

test('account deletion needs a fresh step-up and the handle, then signs out', async ({ page, request, baseURL }) => {
  const { world, login } = await signIn(page, request, baseURL, 'alice');
  await world.advance('6m');
  await page.goto('/settings/delete');
  const button = page.locator('form button[type=submit]');
  await expect(button).toBeDisabled();
  await page.locator('form input').fill('alice');
  await button.click();
  await expect(page.locator('.settings-error[role=alert]')).toBeVisible();
  expect((await me(page.request)).status).toBe(200);

  await login(); // fresh
  await page.reload();
  await page.locator('form input').fill('@Alice');
  await page.locator('form button[type=submit]').click();
  await expect(page).toHaveURL(/\/$/);
  expect((await me(page.request)).status).toBe(404);
  const s = await page.request.get('/api/session', { headers: { 'Sec-Fetch-Site': 'same-origin' } });
  expect(((await s.json()) as { authenticated: boolean }).authenticated).toBe(false);
  // The handle stays taken.
  const again = await request.post('/api/_test/genesis', {
    data: { handle: 'alice' },
    headers: { Origin: baseURL ?? '', 'Sec-Fetch-Site': 'same-origin' },
  });
  expect(again.ok()).toBe(false);
});

test('the moderator recovery page is closed to members and no recovery is pending', async ({ page, request, baseURL }) => {
  await signIn(page, request, baseURL, 'alice');
  await page.goto('/mod/recovery');
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  await expect(page.locator('form')).toHaveCount(0);
  const res = await page.request.get('/api/me/recovery', { headers: { 'Sec-Fetch-Site': 'same-origin' } });
  expect(await res.json()).toEqual({});
  await expect(page.locator('.recovery-banner')).toHaveCount(0);
});
