// P1-W05 end to end, through the real binary with VAPID keys (the e2e server
// gets a fresh pair per run, contract request p1-w05-1) and a LOCAL fake push
// service started by this spec: no real push service is ever contacted. The
// browser's PushManager is stubbed to hand out subscriptions on that fake
// service (keys generated here), so every message the server sends is
// decrypted here (RFC 8291) and its VAPID JWT verified (RFC 8292).
//  1. pia turns push on in /settings/notifications; quinn mentions her: the
//     message is the catalog text in her ui_locale (en, or the en-XA
//     pseudo-locale in the en-XA project), fixed-size, without the post body;
//     with "hide details" on, only the generic text arrives. The service
//     worker shows a pushed message as a notification.
//  2. logging out removes that session's subscription (rex, subscribed too,
//     still receives his).
//  3. rex re-subscribing pia's endpoint moves it: pia's mentions stop
//     arriving there, rex's arrive.
// Selectors avoid English text so the spec runs in en and in en-XA.
import { createDecipheriv, createECDH, createPublicKey, hkdfSync, randomBytes, verify, type ECDH } from 'node:crypto';
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import { expect, test, type APIRequestContext, type Page, type TestInfo } from '@playwright/test';
import { World } from './fixtures/world';

const sameOrigin = { 'Sec-Fetch-Site': 'same-origin' };

// ---- the local fake push service ----

interface Device {
  endpoint: string;
  p256dh: string;
  auth: string;
  ecdh: ECDH;
  authBytes: Buffer;
}

interface Got {
  path: string;
  body: Buffer;
  headers: http.IncomingHttpHeaders;
}

class FakePush {
  private srv = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on('data', (c: Buffer) => chunks.push(c));
    req.on('end', () => {
      this.got.push({ path: req.url ?? '', body: Buffer.concat(chunks), headers: req.headers });
      res.writeHead(201).end();
    });
  });
  got: Got[] = [];
  private n = 0;
  base = '';

  async start(): Promise<void> {
    await new Promise<void>((resolve) => this.srv.listen(0, '127.0.0.1', resolve));
    this.base = `http://127.0.0.1:${String((this.srv.address() as AddressInfo).port)}`;
  }

  stop(): Promise<void> {
    return new Promise((resolve) => this.srv.close(() => { resolve(); }));
  }

  device(): Device {
    const ecdh = createECDH('prime256v1');
    ecdh.generateKeys();
    const authBytes = randomBytes(16);
    this.n++;
    return {
      endpoint: `${this.base}/push/${String(this.n)}-${randomBytes(4).toString('hex')}`,
      p256dh: ecdh.getPublicKey().toString('base64url'),
      auth: authBytes.toString('base64url'),
      ecdh,
      authBytes,
    };
  }

  for(d: Device): Got[] {
    const path = new URL(d.endpoint).pathname;
    return this.got.filter((g) => g.path === path);
  }
}

// decrypt opens one aes128gcm record (RFC 8291 §3.4).
function decrypt(d: Device, body: Buffer): string {
  const salt = body.subarray(0, 16);
  const idlen = body[20] ?? 0;
  const asPub = body.subarray(21, 21 + idlen);
  const ct = body.subarray(21 + idlen);
  const secret = d.ecdh.computeSecret(asPub);
  const info = Buffer.concat([Buffer.from('WebPush: info\0'), d.ecdh.getPublicKey(), asPub]);
  const ikm = Buffer.from(hkdfSync('sha256', secret, d.authBytes, info, 32));
  const cek = Buffer.from(hkdfSync('sha256', ikm, salt, Buffer.from('Content-Encoding: aes128gcm\0'), 16));
  const nonce = Buffer.from(hkdfSync('sha256', ikm, salt, Buffer.from('Content-Encoding: nonce\0'), 12));
  const dec = createDecipheriv('aes-128-gcm', cek, nonce);
  dec.setAuthTag(ct.subarray(ct.length - 16));
  const pt = Buffer.concat([dec.update(ct.subarray(0, ct.length - 16)), dec.final()]);
  let i = pt.length - 1;
  while (i >= 0 && pt[i] === 0) i--;
  expect(pt[i]).toBe(2); // padding delimiter of the last record
  return pt.subarray(0, i).toString('utf8');
}

// verifyVAPID checks the ES256 JWT against the server's public key and the
// endpoint's origin.
function verifyVAPID(auth: string | undefined, serverKey: string, aud: string): void {
  const m = /^vapid t=([^,]+), k=(.+)$/.exec(auth ?? '');
  expect(m, 'vapid authorization').not.toBeNull();
  const [tok, k] = [m?.[1] ?? '', m?.[2] ?? ''];
  expect(k).toBe(serverKey);
  const [h, p, s] = tok.split('.');
  const raw = Buffer.from(k, 'base64url');
  const key = createPublicKey({
    key: { kty: 'EC', crv: 'P-256', x: raw.subarray(1, 33).toString('base64url'), y: raw.subarray(33).toString('base64url') },
    format: 'jwk',
  });
  expect(verify('sha256', Buffer.from(`${h ?? ''}.${p ?? ''}`), { key, dsaEncoding: 'ieee-p1363' }, Buffer.from(s ?? '', 'base64url'))).toBe(true);
  const claims = JSON.parse(Buffer.from(p ?? '', 'base64url').toString()) as { aud: string; exp: number; sub: string };
  expect(claims.aud).toBe(aud);
  expect(claims.exp * 1000).toBeGreaterThan(Date.now());
  expect(claims.sub).toMatch(/^(mailto:|https:)/);
}

// ---- actors ----

class Actor {
  constructor(
    readonly req: APIRequestContext,
    readonly origin: string,
    readonly csrf: string,
  ) {}

  static async login(req: APIRequestContext, origin: string, handle: string): Promise<Actor> {
    return new Actor(req, origin, await new World(req, origin).login(handle));
  }

  async send(method: 'POST' | 'PATCH' | 'DELETE', path: string, data?: unknown): Promise<number> {
    const res = await this.req.fetch(`/api${path}`, {
      method,
      headers: { Origin: this.origin, ...sameOrigin, 'X-PCC-CSRF': this.csrf },
      ...(data === undefined ? {} : { data }),
    });
    return res.status();
  }

  async mention(body: string): Promise<void> {
    expect(await this.send('POST', '/posts', { body, lang: 'en' })).toBe(201);
  }

  async subscribe(d: Device, hide = false): Promise<void> {
    expect(await this.send('POST', '/push/subscriptions', { endpoint: d.endpoint, keys: { p256dh: d.p256dh, auth: d.auth }, hide_content: hide })).toBe(204);
  }
}

const xa = (info: TestInfo) => info.project.name === 'en-XA';

// stubPush makes PushManager hand out d (remembered across reloads).
async function stubPush(page: Page, d: Device): Promise<void> {
  await page.context().grantPermissions(['notifications'], { origin: new URL(page.url() === 'about:blank' ? origin : page.url()).origin });
  await page.addInitScript((dev: { endpoint: string; p256dh: string; auth: string }) => {
    const KEY = 'pcc-e2e-push';
    // Headless browsers may refuse the permission prompt; the page only needs "granted".
    Object.defineProperty(Notification, 'permission', { configurable: true, get: () => 'granted' });
    Notification.requestPermission = () => Promise.resolve('granted');
    const make = () => ({
      endpoint: dev.endpoint,
      expirationTime: null,
      toJSON: () => ({ endpoint: dev.endpoint, expirationTime: null, keys: { p256dh: dev.p256dh, auth: dev.auth } }),
      unsubscribe: () => {
        localStorage.removeItem(KEY);
        return Promise.resolve(true);
      },
    });
    PushManager.prototype.subscribe = function () {
      localStorage.setItem(KEY, '1');
      return Promise.resolve(make() as unknown as PushSubscription);
    };
    PushManager.prototype.getSubscription = function () {
      return Promise.resolve((localStorage.getItem(KEY) ? make() : null) as unknown as PushSubscription | null);
    };
  }, { endpoint: d.endpoint, p256dh: d.p256dh, auth: d.auth });
}

async function setup(request: APIRequestContext, baseURL: string | undefined): Promise<void> {
  const world = new World(request, baseURL ?? '');
  await world.reset();
  await world.seed('p1-w05');
}

async function loginPage(page: Page, baseURL: string | undefined, handle: string, info: TestInfo): Promise<Actor> {
  await page.context().clearCookies();
  await page.goto('/'); // a settled session before anything else
  const a = await Actor.login(page.request, baseURL ?? '', handle);
  if (xa(info)) expect(await a.send('PATCH', '/me/settings', { ui_locale: 'en-XA' })).toBe(200);
  return a;
}

async function turnOn(page: Page): Promise<void> {
  await page.goto('/settings/notifications');
  const settings = page.getByTestId('push-settings');
  await expect(settings).toHaveAttribute('data-subscribed', 'false');
  await page.getByTestId('push-enable').click();
  await expect(settings).toHaveAttribute('data-subscribed', 'true');
}

async function actor(playwright: { request: { newContext(o: object): Promise<APIRequestContext> } }, baseURL: string | undefined, handle: string) {
  const req = await playwright.request.newContext({ baseURL });
  return Actor.login(req, baseURL ?? '', handle);
}

let push: FakePush;
let origin = '';

test.beforeEach(async ({ baseURL }) => {
  origin = baseURL ?? '';
  push = new FakePush();
  await push.start();
});

test.afterEach(async () => {
  await push.stop();
});

test('catalog text in the recipient locale, encrypted and fixed-size; hide details sends only the generic text', async ({ page, playwright, request, baseURL }, info) => {
  await setup(request, baseURL);
  const key = (await (await request.get('/api/push/key', { headers: sameOrigin })).json()) as { enabled: boolean; key: string };
  expect(key.enabled).toBe(true);
  const dev = push.device();
  await stubPush(page, dev);
  await loginPage(page, baseURL, 'pia', info);
  await turnOn(page);

  const quinn = await actor(playwright, baseURL, 'quinn');
  await quinn.mention('Picnic plans by the river with @pia');
  await expect.poll(() => push.for(dev).length, { timeout: 15_000 }).toBe(1);
  const first = push.for(dev)[0];
  if (!first) throw new Error('no push');
  expect(first.headers['content-encoding']).toBe('aes128gcm');
  expect(first.body.length).toBe(4096); // padded: the length says nothing
  verifyVAPID(first.headers.authorization, key.key, push.base);
  const text = decrypt(dev, first.body);
  expect(text).not.toMatch(/Picnic|river/);
  const msg = JSON.parse(text) as { v: number; title: string; tag: string; url: string };
  expect(Object.keys(msg).sort()).toEqual(['tag', 'title', 'url', 'v']);
  expect(msg.url).toBe('/notifications');
  if (xa(info)) {
    expect(msg.title).toMatch(/^\[@quinn ɱêñŧîôñêð ýôû~+\]$/);
  } else {
    expect(msg.title).toBe('@quinn mentioned you');
  }

  // Hide details on the lock screen: only the generic text.
  await page.getByTestId('push-hide').check();
  await expect.poll(async () => {
    const r = await page.request.get(`/api/push/subscriptions?endpoint=${encodeURIComponent(dev.endpoint)}`, { headers: sameOrigin });
    return ((await r.json()) as { hide_content: boolean }).hide_content;
  }).toBe(true);
  await quinn.mention('Bring sandwiches, @pia');
  await expect.poll(() => push.for(dev).length, { timeout: 15_000 }).toBe(2);
  const second = push.for(dev)[1];
  if (!second) throw new Error('no push');
  const generic = xa(info) ? '[Ñêŵ àçŧîṽîŧý~~~~~]' : 'New activity';
  expect(decrypt(dev, second.body)).toBe(JSON.stringify({ v: 1, title: generic, tag: 'activity', url: '/notifications' }));

  // The real service worker shows what it receives (headless browsers refuse
  // to display notifications, so showNotification is observed, not rendered).
  const sw = page.context().serviceWorkers()[0] ?? (await page.context().waitForEvent('serviceworker'));
  const shown = await sw.evaluate(async (payload: string) => {
    interface Scope {
      dispatchEvent(e: Event): boolean;
      registration: { showNotification(title: string, o: { tag?: string; data?: unknown }): Promise<void> };
      PushEvent: new (type: string, init: { data: string }) => Event;
    }
    const scope = globalThis as unknown as Scope;
    const calls: { title: string; tag?: string; data?: unknown }[] = [];
    scope.registration.showNotification = (title, o) => {
      calls.push({ title, tag: o.tag, data: o.data });
      return Promise.resolve();
    };
    scope.dispatchEvent(new scope.PushEvent('push', { data: payload }));
    scope.dispatchEvent(new scope.PushEvent('push', { data: 'not ours' }));
    await new Promise((r) => setTimeout(r, 100));
    return calls;
  }, decrypt(dev, first.body));
  expect(shown).toEqual([{ title: msg.title, tag: msg.tag, data: { url: '/notifications' } }]);
  await quinn.req.dispose();
});

test('logging out removes that session’s subscription', async ({ page, playwright, request, baseURL }, info) => {
  await setup(request, baseURL);
  const dev = push.device();
  await stubPush(page, dev);
  const pia = await loginPage(page, baseURL, 'pia', info);
  await turnOn(page);
  const rex = await actor(playwright, baseURL, 'rex');
  const rexDev = push.device();
  await rex.subscribe(rexDev);

  expect(await pia.send('POST', '/auth/logout')).toBe(204);
  const quinn = await actor(playwright, baseURL, 'quinn');
  await quinn.mention('Hello @pia and @rex');
  await expect.poll(() => push.for(rexDev).length, { timeout: 15_000 }).toBe(1);
  expect(push.for(dev)).toHaveLength(0);

  // Signed in again, the page shows this device as off (the browser still holds it).
  await loginPage(page, baseURL, 'pia', info);
  await page.goto('/settings/notifications');
  await expect(page.getByTestId('push-settings')).toHaveAttribute('data-subscribed', 'false');
  await page.getByTestId('push-enable').click();
  await expect(page.getByTestId('push-settings')).toHaveAttribute('data-subscribed', 'true');
  await rex.req.dispose();
  await quinn.req.dispose();
});

test('re-subscribing an endpoint from another account moves it', async ({ page, playwright, request, baseURL }, info) => {
  await setup(request, baseURL);
  const dev = push.device();
  await stubPush(page, dev);
  await loginPage(page, baseURL, 'pia', info);
  await turnOn(page);

  // Rex, on the same browser, subscribes the same endpoint.
  const rex = await actor(playwright, baseURL, 'rex');
  await rex.subscribe(dev);
  await page.reload();
  await expect(page.getByTestId('push-settings')).toHaveAttribute('data-subscribed', 'false');

  const quinn = await actor(playwright, baseURL, 'quinn');
  await quinn.mention('Only for @pia');
  await quinn.mention('Only for @rex');
  await expect.poll(() => push.for(dev).length, { timeout: 15_000 }).toBe(1);
  await page.waitForTimeout(1500);
  const got = push.for(dev);
  expect(got).toHaveLength(1);
  const msg = JSON.parse(decrypt(dev, got[0]?.body ?? Buffer.alloc(0))) as { title: string };
  expect(msg.title).toBe('@quinn mentioned you'); // rex's locale is en
  await rex.req.dispose();
  await quinn.req.dispose();
});
