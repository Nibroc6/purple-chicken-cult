// P0-W12 end to end: the security, TDM and anti-AI headers on every kind of
// response (HTML, JSON, SSE, assets, 404, robots.txt); the landing and terms
// pages without app data and with the published key fingerprints, with no CSP
// or Trusted Types violation; reporting through the report page answers the
// same whatever the target and the report budget runs out with one uniform 429;
// the moderator queue with the subject's inviter label and probation flag;
// remove_post reaches the inviter's blame inbox with the post they could see;
// suspend_user by one moderator stays pending until a second, freshly verified
// moderator confirms; the public log names people only inside the horizon; CSP
// reports count without trusting a spoofed X-Forwarded-For. Selectors avoid
// English text so the spec runs in en and en-XA (post bodies are user content).
import { expect, test, type APIRequestContext, type Page, type PlaywrightWorkerArgs } from '@playwright/test';
import { World } from './fixtures/world';

interface PostJSON {
  ref: string;
  body: string;
  author: { ref: string; handle: string };
}
interface FeedJSON {
  items: { post?: PostJSON }[];
}
interface ModItemJSON {
  id: string;
  reason: string;
  subject?: { ref: string; handle: string };
  subject_inviter?: { kind: string; handle?: string };
  subject_probation: boolean;
  post?: { body: string };
  awaiting_second: boolean;
}
interface BlameJSON {
  items: { action: string; reason: string; invitee_handle: string; post?: { body: string } }[];
}

const sameOrigin = { 'Sec-Fetch-Site': 'same-origin' };
const garbage = 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA';

async function setup(request: APIRequestContext, baseURL: string | undefined): Promise<World> {
  const world = new World(request, baseURL ?? '');
  await world.reset();
  await world.seed('p0-w12');
  return world;
}

// Actor is a signed-in API client (its own cookie jar).
class Actor {
  constructor(
    readonly req: APIRequestContext,
    readonly origin: string,
    readonly csrf: string,
  ) {}

  static async login(req: APIRequestContext, origin: string, handle: string): Promise<Actor> {
    return new Actor(req, origin, await new World(req, origin).login(handle));
  }

  headers() {
    return { Origin: this.origin, ...sameOrigin, 'X-PCC-CSRF': this.csrf };
  }

  async get<T>(path: string): Promise<T> {
    const res = await this.req.get(`/api${path}`, { headers: sameOrigin });
    expect(res.status(), path).toBe(200);
    return (await res.json()) as T;
  }

  async status(path: string): Promise<number> {
    return (await this.req.get(`/api${path}`, { headers: sameOrigin })).status();
  }

  async send(method: 'POST' | 'PUT' | 'DELETE', path: string, data?: unknown): Promise<{ status: number; body: string }> {
    const res = await this.req.fetch(`/api${path}`, { method, headers: this.headers(), ...(data === undefined ? {} : { data }) });
    return { status: res.status(), body: await res.text() };
  }

  async feedPost(author: string): Promise<PostJSON> {
    const feed = await this.get<FeedJSON>('/feed?preset=latest');
    const p = feed.items.flatMap((it) => (it.post ? [it.post] : [])).find((x) => x.author.handle === author);
    if (!p) throw new Error(`no post by ${author}`);
    return p;
  }

  async report(kind: string, ref: string, reason: string): Promise<{ status: number; body: string }> {
    return this.send('POST', '/reports', { target: { kind, ref }, reason });
  }

  async queue(): Promise<ModItemJSON[]> {
    return (await this.get<{ items: ModItemJSON[] }>('/mod/queue')).items;
  }
}

// actor signs handle in on a request context of its own (separate cookie jar),
// so several people act side by side and the hook client stays cookie-less.
async function actor(pw: PlaywrightWorkerArgs['playwright'], baseURL: string | undefined, handle: string): Promise<Actor> {
  const req = await pw.request.newContext({ baseURL });
  return Actor.login(req, baseURL ?? '', handle);
}

async function loginPage(page: Page, baseURL: string | undefined, handle: string): Promise<Actor> {
  await page.context().clearCookies();
  return Actor.login(page.request, baseURL ?? '', handle);
}

function watchConsole(page: Page): string[] {
  const errors: string[] = [];
  page.on('console', (m) => {
    if (m.type() === 'error') errors.push(m.text());
  });
  page.on('pageerror', (e) => errors.push(e.message));
  return errors;
}

function expectSecurityHeaders(h: Record<string, string>, what: string) {
  expect(h['tdm-reservation'], what).toBe('1');
  expect(h['x-frame-options'], what).toBe('DENY');
  expect(h['x-content-type-options'], what).toBe('nosniff');
  expect(h['x-robots-tag'], what).toContain('noai');
  const csp = h['content-security-policy'] ?? '';
  for (const d of ["default-src 'none'", "script-src 'self'", "frame-ancestors 'none'", "require-trusted-types-for 'script'", 'report-to csp']) {
    expect(csp, `${what}: ${d}`).toContain(d);
  }
}

test('every response carries the CSP, the TDM reservation and X-Frame-Options', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  const html = await request.get('/');
  expect(html.status()).toBe(200);
  expectSecurityHeaders(html.headers(), 'html');
  const asset = /src="(\/assets\/[^"]+\.js)"/.exec(await html.text())?.[1];
  expect(asset).toBeTruthy();
  for (const [what, path, status] of [
    ['asset', asset ?? '', 200],
    ['terms', '/terms', 200],
    ['missing asset', '/assets/nope-123.js', 404],
    ['json', '/api/session', 200],
    ['api 404', '/api/nope', 404],
    ['robots', '/robots.txt', 200],
    ['ai.txt', '/ai.txt', 200],
    ['tdmrep', '/.well-known/tdmrep.json', 200],
    ['keys', '/.well-known/pcc-keys.json', 200],
  ] as const) {
    const res = await request.get(path, { headers: sameOrigin });
    expect(res.status(), what).toBe(status);
    expectSecurityHeaders(res.headers(), what);
  }
  const robots = await (await request.get('/robots.txt')).text();
  expect(robots).toContain('User-agent: *\nDisallow: /');
  for (const bot of ['GPTBot', 'ClaudeBot', 'CCBot', 'Google-Extended', 'PerplexityBot', 'Bytespider', 'Applebot-Extended', 'meta-externalagent']) {
    expect(robots).toContain(`User-agent: ${bot}\nDisallow: /`);
  }
  // The SSE stream: read its headers from the page, then close it.
  await loginPage(page, baseURL, 'carol');
  await page.goto('/terms');
  const sse = await page.evaluate(async () => {
    const ctl = new AbortController();
    const res = await fetch('/api/events', { signal: ctl.signal });
    const h: Record<string, string> = {};
    res.headers.forEach((v, k) => {
      h[k] = v;
    });
    ctl.abort();
    return { status: res.status, h };
  });
  expect(sse.status).toBe(200);
  expect(sse.h['content-type']).toBe('text/event-stream');
  expectSecurityHeaders(sse.h, 'sse');
});

test('landing and terms show no app data and the published key fingerprints', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  const errors = watchConsole(page);
  const api: string[] = [];
  page.on('request', (r) => {
    const p = new URL(r.url()).pathname;
    if (p.startsWith('/api/')) api.push(p);
  });
  await page.goto('/');
  await expect(page.getByTestId('landing-page')).toBeVisible();
  await expect(page.getByTestId('landing-login')).toHaveAttribute('href', '/login');
  await expect(page.getByTestId('landing-join')).toHaveAttribute('href', '/join');
  // Nothing but the session check: no members, posts or counts.
  expect([...new Set(api)]).toEqual(['/api/session']);
  for (const name of ['Alice', 'Bob', 'miracle']) await expect(page.locator('body')).not.toContainText(name);

  await page.getByTestId('landing-terms').click();
  await expect(page.getByTestId('terms-page')).toBeVisible();
  await expect(page.locator('#tdm')).toBeVisible();
  const keys = (await (await request.get('/.well-known/pcc-keys.json')).json()) as { server: { fingerprint: string }[] };
  expect(keys.server.length).toBeGreaterThan(0);
  const fp = keys.server[0]?.fingerprint ?? '';
  await expect(page.getByTestId('terms-fingerprint').first()).toHaveText((fp.match(/.{1,4}/g) ?? []).join(' '));
  await expect(page.getByTestId('site-footer').locator('a[href="/terms"]')).toBeVisible();
  expect(errors).toEqual([]);
});

test('reports answer the same whatever the target and run out with one uniform 429', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  const carol = await loginPage(page, baseURL, 'carol');
  const b1 = await carol.feedPost('bob');

  // Through the report page (what the post actions and inviter labels open).
  for (const ref of [b1.ref, garbage]) {
    const sent = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/reports');
    await page.goto(`/report?kind=post&ref=${ref}`);
    await page.locator('input[type=radio][value=spam]').check();
    await page.getByTestId('report-submit').click();
    expect((await sent).status()).toBe(202);
    await expect(page.getByTestId('report-sent')).toBeVisible();
  }
  await page.goto('/report?kind=account&ref=x');
  await expect(page.getByTestId('report-invalid')).toBeVisible();

  const bad = await carol.report('post', b1.ref, 'rude');
  expect(bad.status).toBe(422);
  expect(JSON.parse(bad.body)).toEqual({ error: 'reason_invalid', field: 'reason' });

  // 3 charged above (the refused reason too); 17 more exhaust the daily budget of 20.
  for (let i = 0; i < 17; i++) expect((await carol.report('person', `${garbage}${String(i)}`, 'spam')).status).toBe(202);
  const valid = await carol.report('post', b1.ref, 'spam');
  const junk = await carol.report('post', garbage, 'spam');
  const invalid = await carol.report('post', b1.ref, 'rude');
  expect(valid.status).toBe(429);
  expect(junk).toEqual(valid);
  expect(invalid).toEqual(valid);
  expect(JSON.parse(valid.body)).toMatchObject({ error: 'rate_limited' });
  // The page says so too, with the wait.
  await page.goto(`/report?kind=post&ref=${b1.ref}`);
  await page.getByTestId('report-submit').click();
  await expect(page.getByTestId('report-error')).toBeVisible();
});

test('moderation: queue, removal to the blame inbox, two-person suspension, public log', async ({ page, request, baseURL, playwright }) => {
  const world = await setup(request, baseURL);
  const carol = await actor(playwright, baseURL, 'carol');
  const b1 = await carol.feedPost('bob');
  const bobRef = b1.author.ref;
  expect((await carol.report('post', b1.ref, 'spam')).status).toBe(202);
  expect((await carol.report('person', bobRef, 'bot')).status).toBe(202);

  // Members have no moderator surface.
  expect(await carol.status('/mod/queue')).toBe(404);

  // mo1: the queue shows the subject with inviter label, and the content.
  await loginPage(page, baseURL, 'mo1');
  await page.goto('/mod');
  const items = page.getByTestId('queue-item');
  await expect(items).toHaveCount(2);
  const spam = page.locator('[data-testid="queue-item"][data-reason="spam"]');
  await expect(spam.getByTestId('queue-post')).toHaveText(b1.body);
  await expect(spam.getByTestId('queue-subject')).toContainText('bob');
  await expect(spam.getByTestId('queue-inviter')).toContainText('alice');
  await spam.getByTestId('queue-action').selectOption('remove_post');
  await spam.getByTestId('queue-reason').selectOption('spam');
  const removed = page.waitForResponse((r) => r.url().includes('/resolve'));
  await spam.getByTestId('queue-resolve').click();
  expect((await removed).status()).toBe(204);
  await expect(items).toHaveCount(1);

  // alice (bob's inviter) gets the entry with the post she could see; the reporter stays hidden.
  const alice = await actor(playwright, baseURL, 'alice');
  const blame = await alice.get<BlameJSON>('/blame');
  expect(blame.items).toHaveLength(1);
  expect(blame.items[0]).toMatchObject({ action: 'remove_post', reason: 'spam', invitee_handle: 'bob', post: { body: b1.body } });
  expect(JSON.stringify(blame)).not.toContain('carol');

  // suspend_user: mo1 starts it (fresh sign-in), the same moderator cannot confirm.
  const bot = page.locator('[data-testid="queue-item"][data-reason="bot"]');
  await bot.getByTestId('queue-action').selectOption('suspend_user');
  await bot.getByTestId('queue-reason').selectOption('bot');
  await bot.getByTestId('queue-resolve').click();
  await expect(page.locator('[data-testid="queue-item"][data-awaiting="true"]')).toHaveCount(1);
  const own = page.waitForResponse((r) => r.url().includes('/confirm'));
  await page.getByTestId('queue-confirm').click();
  expect((await own).status()).toBe(409);
  await expect(page.getByTestId('queue-error')).toBeVisible();

  // One moderator's decision stays pending: bob still works, alice was told nothing more.
  const bob = await actor(playwright, baseURL, 'bob');
  expect(await bob.status('/me')).toBe(200);
  expect((await alice.get<BlameJSON>('/blame')).items).toHaveLength(1);

  // mo2 with a stale passkey time: step-up; after a fresh sign-in: confirmed.
  await loginPage(page, baseURL, 'mo2');
  await world.advance('10m');
  await page.goto('/mod');
  const stale = page.waitForResponse((r) => r.url().includes('/confirm'));
  await page.getByTestId('queue-confirm').click();
  const staleRes = await stale;
  expect(staleRes.status()).toBe(401);
  expect(((await staleRes.json()) as { error: string }).error).toBe('step_up_required');
  await loginPage(page, baseURL, 'mo2');
  await page.goto('/mod');
  const confirmed = page.waitForResponse((r) => r.url().includes('/confirm'));
  await page.getByTestId('queue-confirm').click();
  expect((await confirmed).status()).toBe(204);
  await expect(page.getByTestId('queue-empty')).toBeVisible();

  expect(await bob.status('/me')).toBe(404); // suspended: the session is gone
  const after = await alice.get<BlameJSON>('/blame');
  expect(after.items.map((i) => i.action)).toEqual(['suspend_user', 'remove_post']);

  // alice sees both in her blame inbox page.
  await loginPage(page, baseURL, 'alice');
  await page.goto('/blame');
  await expect(page.getByTestId('blame-item')).toHaveCount(2);
  await expect(page.locator('[data-testid="blame-item"][data-action="remove_post"]').getByTestId('blame-post')).toContainText(b1.body);

  // The public log: carol has bob in her horizon (named), xavier does not (pseudonym).
  await loginPage(page, baseURL, 'carol');
  await page.goto('/mod/log');
  await expect(page.getByTestId('log-row')).toHaveCount(2);
  await expect(page.getByTestId('log-person').first()).toContainText('bob');
  await loginPage(page, baseURL, 'xavier');
  await page.goto('/mod/log');
  await expect(page.getByTestId('log-row')).toHaveCount(2);
  await expect(page.getByTestId('log-person')).toHaveCount(0);
  await expect(page.getByTestId('log-pseudonym')).toHaveCount(2);
});

test('the blame entry leaves out a post the inviter could not see', async ({ request, baseURL, playwright }) => {
  await setup(request, baseURL);
  const frank = await actor(playwright, baseURL, 'frank');
  const e1 = await frank.feedPost('eve');
  expect((await frank.report('post', e1.ref, 'harassment')).status).toBe(202);
  const mo1 = await actor(playwright, baseURL, 'mo1');
  const [item] = await mo1.queue();
  expect(item?.subject?.handle).toBe('eve');
  expect((await mo1.send('POST', `/mod/items/${item?.id ?? ''}/resolve`, { action: 'remove_post', reason: 'harassment' })).status).toBe(204);
  const alice = await actor(playwright, baseURL, 'alice');
  const blame = await alice.get<BlameJSON>('/blame');
  expect(blame.items).toHaveLength(1);
  expect(blame.items[0]?.invitee_handle).toBe('eve');
  expect(blame.items[0]?.post).toBeUndefined();
});

test('CSP reports are counted per network, whatever X-Forwarded-For says', async ({ request, baseURL }) => {
  await setup(request, baseURL);
  const origin = baseURL ?? '';
  const send = async (xff: string) =>
    (
      await request.post('/api/csp-report', {
        headers: { Origin: origin, ...sameOrigin, 'Content-Type': 'application/reports+json', 'X-Forwarded-For': xff },
        data: JSON.stringify([{ type: 'csp-violation', body: { effectiveDirective: 'script-src-elem' } }]),
      })
    ).status();
  for (let i = 0; i < 30; i++) expect(await send(`198.51.100.${String(i)}`)).toBe(204);
  expect(await send('203.0.113.7')).toBe(429);
});
