// P0-W01 end to end: genesis enrollment, passkey login and step-up with
// Chromium's virtual authenticator, through the real binary. Selectors avoid
// English text so the spec runs in en and in the pseudo-locale en-XA.
//
// WebAuthn refuses IP-address origins ("This is an invalid domain"), and the
// shared server of e2e/fixtures/server.ts serves http://127.0.0.1:<port>. Until
// contract request p0-w01-3 switches that fixture to http://localhost:<port>,
// this spec starts its own server the same way (same binary, database and
// static dir that scripts/e2e.sh prepared; a free port) with a localhost origin.
import { spawn, type ChildProcess } from 'node:child_process';
import path from 'node:path';
import { expect, request as pwRequest, test, type APIRequestContext, type Page } from '@playwright/test';
import { addVirtualAuthenticator } from './fixtures/auth';
import { freePort } from './fixtures/server';
import { World } from './fixtures/world';

let child: ChildProcess | undefined;
let origin = '';

async function waitReady(url: string): Promise<void> {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`${url}/api/session`, { headers: { 'Sec-Fetch-Site': 'same-origin' } });
      if (res.ok) return;
    } catch {
      // not up yet
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`pcc did not start at ${url}`);
}

test.beforeAll(async () => {
  const repo = path.resolve(import.meta.dirname, '..', '..');
  const bin = process.env.PCC_E2E_BIN ?? path.join(repo, 'var', process.platform === 'win32' ? 'pcc.exe' : 'pcc');
  const port = await freePort();
  origin = `http://localhost:${String(port)}`;
  child = spawn(bin, ['serve'], {
    cwd: repo,
    stdio: ['ignore', 'inherit', 'inherit'],
    env: {
      ...process.env,
      PCC_ENV: 'test',
      PCC_TEST_HOOKS: '1',
      PCC_HTTP_ADDR: `127.0.0.1:${String(port)}`,
      PCC_ORIGIN: origin,
      PCC_AUTO_MIGRATE: '1',
    },
  });
  await waitReady(`http://127.0.0.1:${String(port)}`);
});

test.afterAll(async () => {
  const c = child;
  if (!c || c.exitCode !== null) return;
  await new Promise<void>((resolve) => {
    c.once('exit', () => {
      resolve();
    });
    c.kill();
  });
});

// hooks drives /api/_test/* from a cookie-less context (no CSRF needed).
async function hooks(): Promise<{ world: World; ctx: APIRequestContext }> {
  const ctx = await pwRequest.newContext({ baseURL: origin });
  return { world: new World(ctx, origin), ctx };
}

async function session(page: Page): Promise<{ authenticated: boolean; me?: { handle: string } }> {
  const res = await page.request.get(`${origin}/api/session`, { headers: { 'Sec-Fetch-Site': 'same-origin' } });
  return (await res.json()) as { authenticated: boolean; me?: { handle: string } };
}

test('genesis enrollment, passkey login and step-up with a virtual authenticator', async ({ page, context }) => {
  const { world, ctx } = await hooks();
  await world.reset();
  const token = await world.genesisToken('alice');
  const authn = await addVirtualAuthenticator(context, page);

  // Enroll: the token travels in the fragment, which the page wipes at once.
  await page.goto(`${origin}/enroll#${token}`);
  await expect(page.getByTestId('enroll-create')).toBeDisabled();
  expect(new URL(page.url()).hash).toBe('');
  await page.locator('input[name=tos]').check();
  await page.locator('input[name=age]').check();
  await page.getByTestId('enroll-create').click();
  await page.getByTestId('second-done').click();
  await expect(page).toHaveURL(`${origin}/`);
  expect((await session(page)).me?.handle).toBe('alice');

  // The link is single use.
  const reuse = await ctx.post('/api/enroll/begin', {
    data: { token },
    headers: { Origin: origin, 'Sec-Fetch-Site': 'same-origin' },
  });
  expect(reuse.status()).toBe(404);

  // Sign out, then sign in with the passkey.
  await page.goto(`${origin}/settings/passkeys`);
  await expect(page.getByTestId('passkey-item')).toHaveCount(1);
  await expect(page.getByTestId('passkey-remove')).toBeDisabled();
  await page.getByTestId('sign-out').click();
  await expect(page).toHaveURL(`${origin}/`);
  expect((await session(page)).authenticated).toBe(false);
  // Passkey autofill (conditional mediation): the virtual authenticator answers
  // the pending request as soon as the login page offers it.
  await page.goto(`${origin}/login`);
  await expect(page).toHaveURL(`${origin}/`);
  expect((await session(page)).me?.handle).toBe('alice');

  // Step-up: after five minutes, adding a passkey asks for a fresh check.
  await world.advance('6m');
  await page.goto(`${origin}/settings/passkeys`);
  await page.getByTestId('passkey-add').click();
  await page.getByTestId('stepup-verify').click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  // The retried add reaches the authenticator, which already holds this
  // account's passkey (excludeCredentials): the page says so.
  await expect(page.locator('.auth-form [role=alert]')).toBeVisible();

  // A second authenticator adds a second passkey (the session is fresh now).
  await authn.remove();
  const second = await addVirtualAuthenticator(context, page);
  await page.locator('.auth-form input[type=text]').fill('Phone');
  await page.getByTestId('passkey-add').click();
  await expect(page.getByTestId('passkey-item')).toHaveCount(2);

  // Removing the passkey this session signed in with ends the session.
  page.once('dialog', (d) => {
    void d.accept();
  });
  await page.getByTestId('passkey-remove').first().click();
  await expect(page).toHaveURL(`${origin}/login`);
  expect((await session(page)).authenticated).toBe(false);

  // The remaining passkey signs in through the button (autofill switched off).
  await page.addInitScript(() => {
    PublicKeyCredential.isConditionalMediationAvailable = () => Promise.resolve(false);
  });
  await page.goto(`${origin}/login`);
  await expect(page.locator('input[autocomplete="username webauthn"]')).toBeVisible();
  await page.getByTestId('login-passkey').click();
  await expect(page).toHaveURL(`${origin}/`);
  expect((await session(page)).me?.handle).toBe('alice');
  await second.remove();
  await ctx.dispose();
});

test('cross-site API requests get the uniform 404 while navigation gets the app', async () => {
  const { world, ctx } = await hooks();
  await world.reset();
  const unknown = await ctx.get('/api/definitely/not/here', { headers: { 'Sec-Fetch-Site': 'same-origin' } });
  const cross = await ctx.get('/api/session', { headers: { 'Sec-Fetch-Site': 'cross-site' } });
  expect(cross.status()).toBe(404);
  expect(await cross.text()).toBe(await unknown.text());
  const login = await ctx.post('/api/auth/login/begin', { data: {}, headers: { Origin: 'https://evil.example' } });
  expect(login.status()).toBe(404);
  expect(await login.text()).toBe(await unknown.text());
  const nav = await ctx.get('/', { headers: { 'Sec-Fetch-Site': 'cross-site', 'Sec-Fetch-Mode': 'navigate' } });
  expect(nav.status()).toBe(200);
  expect(nav.headers()['content-type']).toContain('text/html');
  await ctx.dispose();
});
