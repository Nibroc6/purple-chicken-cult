// P1-W10 end to end, through the real binary:
//  1. Guardian recovery released: alice lost every passkey. gina (friend+)
//     starts her recovery at /settings/recovery; the recovering device — a
//     browser whose fake camera films gina's code, without any session of
//     alice — scans it at /recover, the SAS matches and the sound passes. gus
//     (alice's other friend+) approves with his passkey (co-sign dialog,
//     RecoveryApproval record); pat (a plain friend) sees nothing and gets the
//     uniform 404. After the 24 h hold the device enrolls a new passkey for
//     alice and every old session of hers is gone.
//  2. Cancel during the hold: alice's signed-in browser shows the banner and
//     cancels; the device then reads cancelled.
//  3. Co-signed deletion: a passkey session deletes its account through the
//     co-sign dialog (AccountDelete record).
// Selectors avoid English text so the spec runs in en and in en-XA.
import { expect, request as pwRequest, test, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { addVirtualAuthenticator } from './fixtures/auth';
import { installListener, installPlayer, launchWithCamera, relaySound, type CameraBrowser } from './fixtures/camera';
import { World } from './fixtures/world';

const sameOrigin = { 'Sec-Fetch-Site': 'same-origin' };
let origin = '';

test.beforeEach(({ baseURL }) => {
  origin = baseURL ?? '';
});

async function world(): Promise<World> {
  return new World(await pwRequest.newContext({ baseURL: origin }), origin);
}

// enroll makes handle a genesis account with a passkey in page.
async function enroll(page: Page, context: BrowserContext, w: World, handle: string): Promise<void> {
  await addVirtualAuthenticator(context, page);
  // Load the app once first: the enrollment page then starts from a settled session.
  await page.goto(`${origin}/`);
  await page.waitForLoadState('networkidle');
  const token = await w.genesisToken(handle);
  await page.goto(`${origin}/enroll#${token}`);
  await page.locator('input[name=tos]').check();
  await page.locator('input[name=age]').check();
  await page.getByTestId('enroll-create').click();
  await page.getByTestId('second-done').click();
  await expect(page).toHaveURL(`${origin}/`);
}

// browserFor opens a new signed-out browser context and page in the run's locale.
async function browserFor(browser: Browser, locale: string | undefined): Promise<{ ctx: BrowserContext; page: Page }> {
  const ctx = await browser.newContext({ baseURL: origin, ...(locale ? { locale } : {}) });
  return { ctx, page: await ctx.newPage() };
}

// sign signs the open co-sign dialog of page, which must show a record of kind.
async function sign(page: Page, kind: string): Promise<void> {
  const dialog = page.getByTestId('cosign-dialog');
  await expect(dialog.getByTestId('cosign-record')).toHaveAttribute('data-kind', kind);
  await dialog.getByTestId('cosign-sign').click();
  await expect(dialog).toHaveCount(0);
}

// csrf reads the CSRF token of page's session.
async function csrf(page: Page): Promise<string> {
  const res = await page.request.get('/api/session', { headers: sameOrigin });
  return ((await res.json()) as { csrf: string }).csrf;
}

interface Cast {
  w: World;
  gina: Page; // guardian A, in person with the recovering person
  gus: Page; // guardian B
  pat: Page; // a plain friend of alice
  alice: Page; // alice's own signed-in browser (no passkey left to use)
}

// cast enrolls the guardians, signs alice and pat in through the test hooks
// and seeds the friendships.
async function cast(page: Page, context: BrowserContext, browser: Browser, locale: string | undefined): Promise<Cast> {
  const w = await world();
  await w.reset();
  await installListener(page);
  await enroll(page, context, w, 'gina');
  const gus = await browserFor(browser, locale);
  await enroll(gus.page, gus.ctx, w, 'gus');
  const pat = await browserFor(browser, locale);
  const alice = await browserFor(browser, locale);
  await w.genesisToken('alice');
  await new World(alice.page.request, origin).login('alice');
  await w.seed('p1-w10');
  await new World(pat.page.request, origin).login('pat');
  return { w, gina: page, gus: gus.page, pat: pat.page, alice: alice.page };
}

// vouch runs the in-person part: gina starts alice's recovery, the recovering
// device (a camera browser) scans, the codes match and the sound passes. It
// returns the camera browser and the device's page, following the recovery.
async function vouch(c: Cast, locale: string | undefined): Promise<{ cam: CameraBrowser; device: Page }> {
  await c.gina.goto(`${origin}/settings/recovery`);
  const friend = c.gina.locator('[data-testid=recovery-friend][data-handle=alice]');
  const frame = c.gina.waitForResponse((r) => /\/api\/invites\/handshakes\/[^/]+\/frame$/.test(r.url()) && r.ok());
  const started = c.gina.waitForResponse((r) => new URL(r.url()).pathname === '/api/recovery/sessions');
  await friend.getByTestId('recovery-start').click();
  expect((await started).status()).toBe(201);
  const { payload } = (await (await frame).json()) as { payload: string };
  await expect(c.gina.getByTestId('invite-handshake')).toHaveAttribute('data-kind', 'recovery');

  const cam = await launchWithCamera(payload);
  const ctx = await cam.browser.newContext({ baseURL: origin, ...(locale ? { locale } : {}) });
  const device = await ctx.newPage();
  await addVirtualAuthenticator(ctx, device);
  await installPlayer(device);
  await device.goto(`${origin}/recover`);
  const claimed = device.waitForResponse((r) => new URL(r.url()).pathname === '/api/recovery/claim');
  await device.getByTestId('recover-scan').click();
  expect((await claimed).status()).toBe(200);
  const sas = device.getByTestId('join-sas');
  await expect(sas).toHaveText(/^[0-9]{6}$/, { timeout: 20_000 });
  await expect(c.gina.getByTestId('invite-sas')).toHaveText((await sas.textContent()) ?? '');
  await c.gina.getByTestId('invite-match').click();
  await expect(c.gina.getByTestId('invite-listening')).toBeVisible();
  await device.getByTestId('join-play').click();
  await relaySound(device, c.gina, { ultra: true, jingle: true });
  // A passed recovery handshake is done at once (nothing for gina to sign).
  await expect(c.gina.getByTestId('invite-done')).toBeVisible();
  await expect(device.getByTestId('recover-state')).toHaveAttribute('data-state', 'approving', { timeout: 20_000 });
  return { cam, device };
}

// approve is gus's co-signed approval of alice's recovery.
async function approve(gus: Page): Promise<void> {
  await gus.goto(`${origin}/settings/recovery`);
  const item = gus.locator('[data-testid=recovery-request][data-handle=alice]');
  await item.getByTestId('recovery-approve').click();
  await sign(gus, 'recovery_approval');
  await expect(item.getByTestId('recovery-approved')).toBeVisible();
}

test('a guardian recovery: in person, a second guardian co-signs, released after the hold', async ({ page, context, browser, locale }) => {
  test.setTimeout(150_000);
  const c = await cast(page, context, browser, locale);
  const { cam, device } = await vouch(c, locale);
  try {
    // A plain friend sees nothing to approve and cannot approve.
    const patList = await c.pat.request.get('/api/recovery/requests', { headers: sameOrigin });
    expect(await patList.json()).toEqual({ items: [] });
    // gina vouched already; she is not the second guardian.
    await c.gina.goto(`${origin}/settings/recovery`);
    await expect(c.gina.getByTestId('recovery-approvals').getByTestId('recovery-request')).toHaveCount(0);

    // gus's ref of the request is his alone: pat using it is the uniform 404.
    const gusList = (await (await c.gus.request.get('/api/recovery/requests', { headers: sameOrigin })).json()) as {
      items: { id: string; account: { handle: string } }[];
    };
    expect(gusList.items.map((i) => i.account.handle)).toEqual(['alice']);
    const id = gusList.items[0]?.id ?? '';
    const patTry = await c.pat.request.post(`/api/recovery/requests/${id}/approve`, {
      headers: { ...sameOrigin, Origin: origin, 'X-PCC-CSRF': await csrf(c.pat) },
    });
    expect(patTry.status()).toBe(404);

    await approve(c.gus);
    await expect(device.getByTestId('recover-state')).toHaveAttribute('data-state', 'holding', { timeout: 20_000 });
    // alice's own browser shows the banner during the hold.
    await c.alice.goto(`${origin}/`);
    await expect(c.alice.locator('.recovery-banner')).toBeVisible();

    // After the hold the device gets the enrollment link and enrolls alice.
    await c.w.advance('24h1m');
    await device.reload();
    const enroll = device.getByTestId('recover-enroll');
    await expect(enroll).toBeVisible({ timeout: 20_000 });
    await enroll.click();
    await device.getByTestId('enroll-create').click();
    await device.getByTestId('second-done').click();
    await expect(device).toHaveURL(`${origin}/`);
    const sv = (await (await device.request.get('/api/session', { headers: sameOrigin })).json()) as {
      authenticated: boolean;
      me?: { handle: string };
    };
    expect(sv.authenticated).toBe(true);
    expect(sv.me?.handle).toBe('alice');
    // alice's old session ended with the release.
    const old = await c.alice.request.get('/api/me', { headers: sameOrigin });
    expect(old.status()).toBe(404);
  } finally {
    await cam.close();
  }
});

test('the account cancels a guardian recovery during the hold', async ({ page, context, browser, locale }) => {
  test.setTimeout(120_000);
  const c = await cast(page, context, browser, locale);
  const { cam, device } = await vouch(c, locale);
  try {
    await approve(c.gus);
    await c.alice.goto(`${origin}/`);
    const banner = c.alice.locator('.recovery-banner');
    await expect(banner).toBeVisible();
    await banner.getByRole('button').click();
    await expect(c.alice.locator('.recovery-banner[role=status]')).toBeVisible();
    await device.reload();
    await expect(device.getByTestId('recover-state')).toHaveAttribute('data-state', 'cancelled', { timeout: 20_000 });
    await c.w.advance('24h1m');
    await device.reload();
    await expect(device.getByTestId('recover-state')).toHaveAttribute('data-state', 'cancelled');
    await expect(device.getByTestId('recover-enroll')).toHaveCount(0);
  } finally {
    await cam.close();
  }
});

test('a passkey session deletes its account with a co-signature', async ({ page, context }) => {
  const w = await world();
  await w.reset();
  await enroll(page, context, w, 'dora');
  await page.goto(`${origin}/settings/delete`);
  await page.locator('form input').fill('dora');
  const deleted = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/me/delete');
  await page.locator('form button[type=submit]').click();
  await sign(page, 'account_delete');
  expect((await deleted).status()).toBe(202);
  await expect(page).toHaveURL(`${origin}/`);
  const sv = (await (await page.request.get('/api/session', { headers: sameOrigin })).json()) as { authenticated: boolean };
  expect(sv.authenticated).toBe(false);
});
