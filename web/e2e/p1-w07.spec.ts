// P1-W07 end to end, through the real binary (PCC_DBSC=1, fake server clock):
//  1. A keyboard-only and a screen-reader profile (automated browsers, so
//     navigator.webdriver is set) read for twelve minutes at human pace and
//     never get a step-up.
//  2. A bot — webdriver and a sustained page rate without idle minutes —
//     must step up: every request answers step_up_required, the app explains
//     why, and one passkey tap clears it. Its account was enrolled from a
//     fresh browser that went straight to /enroll#token (the CSRF race).
//  3. Device-bound sessions, driven by a software device key (Playwright's
//     Chromium has no DBSC; this is the local dev driver of the protocol): the
//     long-lived cookie replayed from another browser fails, a bound cookie
//     past 10 minutes fails, a refresh proof works once, an RS256 key registers.
//  4. A padded route answers found and not-found in the same 150 ms bucket.
// Selectors avoid English text so the spec runs in en and in en-XA.
import { webcrypto } from 'node:crypto';
import { expect, request as pwRequest, test, type APIRequestContext, type Page } from '@playwright/test';
import { addVirtualAuthenticator } from './fixtures/auth';
import { World } from './fixtures/world';

const sameOrigin = { 'Sec-Fetch-Site': 'same-origin' };
let origin = '';

test.beforeEach(({ baseURL }) => {
  origin = baseURL ?? '';
});

async function world(): Promise<World> {
  const w = new World(await pwRequest.newContext({ baseURL: origin }), origin);
  await w.reset();
  await w.seed('p1-w07');
  return w;
}

interface SessionJSON {
  authenticated: boolean;
  csrf?: string;
  step_up?: boolean;
}

async function session(req: APIRequestContext): Promise<SessionJSON> {
  return (await (await req.get(`${origin}/api/session`, { headers: sameOrigin })).json()) as SessionJSON;
}

// ---- 1. assistive profiles ----

// readHuman reads like a person for a minute of server time: keys only (no
// pointer), a page load, then the clock moves on.
async function readMinute(page: Page, w: World, keys: string[]): Promise<void> {
  await page.goto(`${origin}/`);
  await expect(page.getByTestId('feed-layout')).toBeVisible();
  for (const k of keys) await page.keyboard.press(k);
  // Leaving the page flushes the signal report (counts of the keys above).
  await page.evaluate(() => {
    globalThis.dispatchEvent(new Event('pagehide'));
  });
  await w.advance('1m');
}

test('keyboard-only and screen-reader profiles never trigger a step-up', async ({ browser }) => {
  test.setTimeout(120_000);
  const w = await world();
  const kb = await browser.newContext({ baseURL: origin });
  const sr = await browser.newContext({ baseURL: origin, reducedMotion: 'reduce', forcedColors: 'active' });
  const kbPage = await kb.newPage();
  const srPage = await sr.newPage();
  await new World(kbPage.request, origin).login('kim');
  await new World(srPage.request, origin).login('rae');
  expect(await kbPage.evaluate(() => navigator.webdriver)).toBe(true);
  for (let m = 0; m < 12; m++) {
    await readMinute(kbPage, w, ['Tab', 'Tab', 'Tab', 'Enter', 'Shift+Tab', 'PageDown', 'End', 'Home']);
    // The screen reader walks the accessibility tree item by item.
    // (Read posts drop out of later feed sessions: the first one has them.)
    await srPage.goto(`${origin}/`);
    await expect(srPage.getByTestId('feed-layout')).toBeVisible();
    if (m === 0) await expect(srPage.locator('main')).toContainText('Bread');
    expect((await srPage.locator('main').ariaSnapshot()).length).toBeGreaterThan(0);
    await readMinute(srPage, w, ['ArrowDown', 'ArrowDown', 'ArrowDown', 'Tab', 'ArrowDown', 'Tab']);
  }
  for (const p of [kbPage, srPage]) {
    expect((await session(p.request)).step_up ?? false).toBe(false);
    await p.goto(`${origin}/`);
    await expect(p.getByTestId('feed-layout')).toBeVisible();
    await expect(p.getByRole('dialog')).toHaveCount(0);
  }
  await kb.close();
  await sr.close();
});

// ---- 2. webdriver + sustained rate ----

test('webdriver and a sustained page rate ask for a passkey step-up, which one tap clears', async ({ browser }) => {
  test.setTimeout(120_000);
  const w = await world();
  // A fresh browser goes straight to its enrollment link (no earlier page load).
  const ctx = await browser.newContext({ baseURL: origin });
  const page = await ctx.newPage();
  const authn = await addVirtualAuthenticator(ctx, page);
  const token = await w.genesisToken('robo');
  await page.goto(`${origin}/enroll#${token}`);
  await page.locator('input[name=tos]').check();
  await page.locator('input[name=age]').check();
  await page.getByTestId('enroll-create').click();
  await page.getByTestId('second-done').click();
  await expect(page).toHaveURL(`${origin}/`);
  const csrf = (await session(page.request)).csrf ?? '';

  // The bot: ten minutes, eight feed pages each, no input, webdriver reported.
  for (let m = 0; m < 10; m++) {
    if (m > 0) await w.advance('1m');
    const statuses = await page.evaluate(async (token) => {
      const signals = await fetch('/api/signals', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-PCC-CSRF': token },
        body: JSON.stringify({ webdriver: navigator.webdriver, cdp: false, inputs_per_min: [0], visible_ms: 60000 }),
      });
      const pages = await Promise.all(Array.from({ length: 8 }, () => fetch('/api/feed')));
      return [signals.status, ...pages.map((r) => r.status)];
    }, csrf);
    expect(statuses[0]).toBe(204);
  }
  const blocked = await page.request.get(`${origin}/api/feed`, { headers: sameOrigin });
  expect(blocked.status()).toBe(401);
  expect(await blocked.json()).toEqual({ error: 'step_up_required' });
  expect((await session(page.request)).step_up).toBe(true);

  // The app explains and a passkey tap clears it; the feed comes back.
  await page.goto(`${origin}/`);
  const dialog = page.getByRole('dialog');
  await expect(dialog).toHaveAttribute('data-reason', 'signals');
  await page.getByTestId('stepup-verify').click();
  await expect(dialog).toHaveCount(0);
  expect((await session(page.request)).step_up ?? false).toBe(false);
  expect((await page.request.get(`${origin}/api/feed`, { headers: sameOrigin })).status()).toBe(200);
  await authn.remove();
  await ctx.close();
});

// ---- 3. device-bound sessions ----

const subtle = webcrypto.subtle;
const b64u = (b: ArrayBuffer | Uint8Array) => Buffer.from(b instanceof Uint8Array ? b : new Uint8Array(b)).toString('base64url');

// Device is a software device key answering DBSC challenges like a TPM-backed browser.
class Device {
  private constructor(
    readonly alg: 'ES256' | 'RS256',
    private readonly keys: CryptoKeyPair,
  ) {}

  static async create(alg: 'ES256' | 'RS256'): Promise<Device> {
    const params =
      alg === 'ES256'
        ? { name: 'ECDSA', namedCurve: 'P-256' }
        : { name: 'RSASSA-PKCS1-v1_5', modulusLength: 2048, publicExponent: new Uint8Array([1, 0, 1]), hash: 'SHA-256' };
    return new Device(alg, (await subtle.generateKey(params, false, ['sign', 'verify'])) as CryptoKeyPair);
  }

  async jwk(): Promise<Record<string, string>> {
    const j = await subtle.exportKey('jwk', this.keys.publicKey);
    return alg(this.alg, j);
  }

  async sign(payload: Record<string, unknown>): Promise<string> {
    const input = `${b64u(Buffer.from(JSON.stringify({ alg: this.alg, typ: 'dbsc+jwt' })))}.${b64u(Buffer.from(JSON.stringify(payload)))}`;
    const algo = this.alg === 'ES256' ? { name: 'ECDSA', hash: 'SHA-256' } : { name: 'RSASSA-PKCS1-v1_5' };
    const sig = await subtle.sign(algo, this.keys.privateKey, Buffer.from(input));
    return `${input}.${b64u(sig)}`;
  }
}

function alg(a: string, j: webcrypto.JsonWebKey): Record<string, string> {
  return a === 'ES256' ? { kty: 'EC', crv: 'P-256', x: j.x ?? '', y: j.y ?? '' } : { kty: 'RSA', n: j.n ?? '', e: j.e ?? '' };
}

const dbscHeaders = () => ({ ...sameOrigin, Origin: origin }); // the browser's own call: no CSRF header

async function register(req: APIRequestContext, dev: Device): Promise<number> {
  const res = await req.get(`${origin}/api/session`, { headers: sameOrigin });
  const m = /challenge="([A-Za-z0-9_-]+)"/.exec(res.headers()['sec-session-registration'] ?? '');
  expect(m, 'registration offer').not.toBeNull();
  const jwt = await dev.sign({ aud: `${origin}/api/auth/dbsc/register`, jti: m?.[1], iat: Math.floor(Date.now() / 1000), key: await dev.jwk() });
  return (await req.post(`${origin}/api/auth/dbsc/register`, { headers: { ...dbscHeaders(), 'Sec-Session-Response': jwt } })).status();
}

async function refresh(req: APIRequestContext, dev: Device): Promise<{ status: number; jwt: string }> {
  const first = await req.post(`${origin}/api/auth/dbsc/refresh`, { headers: dbscHeaders() });
  if (first.status() !== 401) return { status: first.status(), jwt: '' };
  const m = /^"([A-Za-z0-9_-]+)";id="([^"]+)"$/.exec(first.headers()['sec-session-challenge'] ?? '');
  expect(m, 'refresh challenge').not.toBeNull();
  const jwt = await dev.sign({ aud: `${origin}/api/auth/dbsc/refresh`, jti: m?.[1], sub: m?.[2], iat: Math.floor(Date.now() / 1000) });
  const res = await req.post(`${origin}/api/auth/dbsc/refresh`, { headers: { ...dbscHeaders(), 'Sec-Session-Response': jwt } });
  return { status: res.status(), jwt };
}

const feedStatus = async (req: APIRequestContext) => (await req.get(`${origin}/api/feed`, { headers: sameOrigin })).status();

test('a device-bound session: copied cookies, the 10-minute bound cookie, single-use refresh, RS256', async ({ browser }) => {
  test.setTimeout(90_000);
  const w = await world();
  const a = await pwRequest.newContext({ baseURL: origin });
  await new World(a, origin).login('dee');
  const dev = await Device.create('ES256');
  expect(await register(a, dev)).toBe(200);
  const cookies = (await a.storageState()).cookies;
  expect(cookies.map((c) => c.name).sort()).toEqual(['pcc_dev', 'pcc_dev_b']);
  expect(await feedStatus(a)).toBe(200);

  // The long-lived cookie replayed from another browser: nothing.
  const other = await browser.newContext({ baseURL: origin });
  await other.addCookies(cookies.filter((c) => c.name === 'pcc_dev'));
  const page = await other.newPage();
  expect(await feedStatus(page.request)).toBe(404);
  expect((await session(page.request)).authenticated).toBe(false);
  await page.goto(`${origin}/settings/passkeys`);
  await expect(page.getByTestId('signin-prompt')).toBeVisible();
  // …and it cannot refresh without the device key.
  expect((await refresh(page.request, await Device.create('ES256'))).status).toBe(404);

  // Past 10 minutes the bound cookie fails until the device refreshes.
  await w.advance('11m');
  expect(await feedStatus(a)).toBe(404);
  const r = await refresh(a, dev);
  expect(r.status).toBe(200);
  expect(await feedStatus(a)).toBe(200);
  // A replayed refresh proof fails.
  const replay = await a.post(`${origin}/api/auth/dbsc/refresh`, { headers: { ...dbscHeaders(), 'Sec-Session-Response': r.jwt } });
  expect(replay.status()).toBe(404);

  // An RSA-only TPM's key registers.
  const b = await pwRequest.newContext({ baseURL: origin });
  await new World(b, origin).login('ria');
  expect(await register(b, await Device.create('RS256'))).toBe(200);
  expect(await feedStatus(b)).toBe(200);
  await other.close();
  await a.dispose();
  await b.dispose();
});

// ---- 4. timing padding ----

test("a padded route's found and not-found answers fall in the same bucket", async () => {
  await world();
  const req = await pwRequest.newContext({ baseURL: origin });
  await new World(req, origin).login('pat');
  const feed = (await (await req.get(`${origin}/api/feed`, { headers: sameOrigin })).json()) as {
    items: { post?: { ref: string } }[];
  };
  const ref = feed.items.find((i) => i.post)?.post?.ref ?? '';
  expect(ref).not.toBe('');
  const time = async (path: string, want: number) => {
    const start = performance.now();
    const res = await req.get(`${origin}/api${path}`, { headers: sameOrigin });
    expect(res.status()).toBe(want);
    return performance.now() - start;
  };
  const found: number[] = [];
  const missing: number[] = [];
  for (let i = 0; i < 6; i++) {
    found.push(await time(`/posts/${ref}`, 200));
    missing.push(await time(`/posts/${ref.slice(0, -2)}xx`, 404));
  }
  // Every answer takes one 150 ms bucket: the same for found and not-found.
  const bucket = (d: number) => Math.floor(d / 150);
  for (const d of [...found, ...missing]) expect(bucket(d), `${String(d)} ms`).toBe(1);
  await req.dispose();
});
