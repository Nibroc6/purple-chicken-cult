// P0-W01 end to end: genesis enrollment, passkey login and step-up with
// Chromium's virtual authenticator, through the real binary. Selectors avoid
// English text so the spec runs in en and in the pseudo-locale en-XA.
//
// WebAuthn refuses IP-address origins; the shared server of
// e2e/fixtures/server.ts serves http://localhost:<port> (contract request p0-w01-3).
import { expect, request as pwRequest, test, type APIRequestContext, type Page } from '@playwright/test';
import { addVirtualAuthenticator } from './fixtures/auth';
import { World } from './fixtures/world';

let origin = '';

test.beforeEach(({ baseURL }) => {
  origin = baseURL ?? '';
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

  // From here on autofill is off, so the login below goes through the button.
  await page.addInitScript(() => {
    PublicKeyCredential.isConditionalMediationAvailable = () => Promise.resolve(false);
  });

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
  // P1 (P1-W07): the session was started with a passkey, so the new one is
  // co-signed by the existing passkey; that key moves to the new
  // authenticator for the signature (one virtual authenticator answers at a time).
  const cdp = await context.newCDPSession(page);
  const { credentials: old } = await cdp.send('WebAuthn.getCredentials', { authenticatorId: authn.id });
  await authn.remove();
  await cdp.send('WebAuthn.enable');
  const { authenticatorId: secondId } = await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true },
  });
  const second = {
    id: secondId,
    remove: async () => {
      await cdp.send('WebAuthn.removeVirtualAuthenticator', { authenticatorId: secondId });
    },
  };
  await page.locator('.auth-form input[type=text]').fill('Phone');
  await page.getByTestId('passkey-add').click();
  await expect(page.getByTestId('cosign-dialog')).toBeVisible();
  // As a non-discoverable credential: the authenticator keeps one resident
  // credential per account, the new passkey; the co-sign request names the old one.
  for (const c of old) {
    const credential = { credentialId: c.credentialId, isResidentCredential: false, rpId: c.rpId, privateKey: c.privateKey, signCount: c.signCount };
    await cdp.send('WebAuthn.addCredential', { authenticatorId: second.id, credential });
  }
  await page.getByTestId('cosign-sign').click();
  await expect(page.getByTestId('passkey-item')).toHaveCount(2);
  // The old key leaves the new authenticator again: from here on it holds only the new passkey.
  for (const c of old) {
    await cdp.send('WebAuthn.removeCredential', { authenticatorId: second.id, credentialId: c.credentialId });
  }

  // Removing the passkey this session signed in with ends the session (the
  // removal is co-signed too).
  page.once('dialog', (d) => {
    void d.accept();
  });
  await page.getByTestId('passkey-remove').first().click();
  await page.getByTestId('cosign-sign').click();
  await expect(page).toHaveURL(`${origin}/login`);
  expect((await session(page)).authenticated).toBe(false);

  // The remaining passkey signs in through the button (autofill is off).
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
