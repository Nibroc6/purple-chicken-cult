// P1-W06 end to end, through the real binary:
//  1. Two moderators with passkeys (pk1, pk2: genesis accounts granted with
//     `pcc mod grant`): pk1 starts a suspension of bob; pk2's confirmation
//     opens the co-sign dialog with the exact ModAction record and is signed
//     with pk2's own passkey; only then is bob suspended, alice (his inviter)
//     blamed and marked "invited a removed account" — visible to carol, who had
//     bob in her horizon, never to alice herself.
//  2. A blocklisted upload is held: the queue shows the CSAM item by its match
//     metadata only (no image, no link, no media URL anywhere in the answer);
//     the NCMEC report is recorded only once a second moderator confirms it.
//  3. Invite-tree quarantine: three of root's invitees are suspended by two
//     moderators; the queue item about the fourth offers the quarantine; once
//     a second moderator confirms, root's whole subtree is back on probation
//     and root's invites pause — root's sibling is untouched.
// Selectors avoid English text so the spec runs in en and in en-XA.
import { execFile } from 'node:child_process';
import path from 'node:path';
import { promisify } from 'node:util';
import { expect, request as pwRequest, test, type APIRequestContext, type BrowserContext, type Page } from '@playwright/test';
import { addVirtualAuthenticator } from './fixtures/auth';
import { World } from './fixtures/world';

const sameOrigin = { 'Sec-Fetch-Site': 'same-origin' };
let origin = '';

test.beforeEach(({ baseURL }) => {
  origin = baseURL ?? '';
});

async function world(): Promise<World> {
  return new World(await pwRequest.newContext({ baseURL: origin }), origin);
}

// Actor is a signed-in API client with a cookie jar of its own (test-hook
// session: no passkey, so its two-person steps use the fresh sign-in).
class Actor {
  constructor(
    readonly req: APIRequestContext,
    readonly csrf: string,
  ) {}

  static async as(handle: string): Promise<Actor> {
    const req = await pwRequest.newContext({ baseURL: origin });
    return new Actor(req, await new World(req, origin).login(handle));
  }

  async get<T>(p: string): Promise<T> {
    const res = await this.req.get(`${origin}/api${p}`, { headers: sameOrigin });
    expect(res.status(), p).toBe(200);
    return (await res.json()) as T;
  }

  async status(p: string): Promise<number> {
    return (await this.req.get(`${origin}/api${p}`, { headers: sameOrigin })).status();
  }

  async post(p: string, data?: unknown): Promise<number> {
    const res = await this.req.post(`${origin}/api${p}`, {
      headers: { ...sameOrigin, Origin: origin, 'X-PCC-CSRF': this.csrf },
      ...(data === undefined ? {} : { data }),
    });
    return res.status();
  }

  async queue(): Promise<ItemJSON[]> {
    return (await this.get<{ items: ItemJSON[] }>('/mod/queue')).items;
  }

  async friend(handle: string): Promise<PersonJSON> {
    const p = (await this.get<{ items: PersonJSON[] }>('/friends')).items.find((x) => x.handle === handle);
    if (!p) throw new Error(`no friend ${handle}`);
    return p;
  }
}

interface PersonJSON {
  ref: string;
  handle: string;
  invited_removed?: boolean;
}
interface ItemJSON {
  id: string;
  source: string;
  reason: string;
  subject?: { ref: string; handle: string };
  summary?: Record<string, number>;
  csam?: { match_info: Record<string, string>; reported_at?: string; report_ref?: string };
  pending?: { action: string };
  awaiting_second: boolean;
}

const about = (items: ItemJSON[], handle: string) => items.find((i) => i.subject?.handle === handle);

// grant makes handle a moderator with the real CLI (the first one bootstraps
// as admin), then refreshes the server's user cache through a throwaway
// test-hook sign-in.
async function grant(handle: string, by?: string): Promise<void> {
  const repo = path.resolve(import.meta.dirname, '..', '..');
  const bin = process.env.PCC_E2E_BIN ?? path.join(repo, 'var', 'pcc');
  await promisify(execFile)(bin, ['mod', 'grant', '--handle', handle, ...(by ? ['--by', by] : ['--bootstrap'])], {
    cwd: repo,
    env: { ...process.env, PCC_ENV: 'test', PCC_ORIGIN: origin },
  });
  const ctx = await pwRequest.newContext({ baseURL: origin });
  await new World(ctx, origin).login(handle);
  await ctx.dispose();
}

// enroll makes handle a genesis account with a passkey in page.
async function enroll(page: Page, context: BrowserContext, w: World, handle: string): Promise<void> {
  await addVirtualAuthenticator(context, page);
  // Load the app once first: the enrollment page then starts from a settled
  // session (a fresh context's first load can race the CSRF token).
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

// queueItem is the queue entry about handle on page.
const queueItem = (page: Page, handle: string) =>
  page.getByTestId('queue-item').filter({ has: page.getByTestId('queue-subject-ref') }).filter({ hasText: handle });

// suspend runs a whole two-person suspension of handle through the API.
async function suspend(reporter: Actor, mo1: Actor, mo2: Actor, handle: string): Promise<void> {
  const target = await reporter.friend(handle);
  expect(await reporter.post('/reports', { target: { kind: 'person', ref: target.ref }, reason: 'bot' })).toBe(202);
  const first = about(await mo1.queue(), handle);
  expect(await mo1.post(`/mod/items/${first?.id ?? ''}/resolve`, { action: 'suspend_user', reason: 'bot' })).toBe(204);
  const second = about(await mo2.queue(), handle);
  expect(await mo2.post(`/mod/items/${second?.id ?? ''}/confirm`)).toBe(204);
}

test('the second moderator co-signs the suspension; blame and the removed-account mark follow it', async ({
  page,
  context,
  browser,
  locale,
}) => {
  test.setTimeout(120_000);
  const w = await world();
  await w.reset();
  await enroll(page, context, w, 'pkone');
  const ctx2 = await browser.newContext({ baseURL: origin, ...(locale ? { locale } : {}) });
  const page2 = await ctx2.newPage();
  try {
    await enroll(page2, ctx2, w, 'pktwo');
    await grant('pkone');
    await grant('pktwo', 'pkone');
    await w.seed('p1-w06');

    const carol = await Actor.as('carol');
    const bob = await Actor.as('bob');
    const bobRef = (await carol.friend('bob')).ref;
    expect(await carol.post('/reports', { target: { kind: 'person', ref: bobRef }, reason: 'bot' })).toBe(202);

    // pkone starts the suspension (just enrolled: a fresh passkey sign-in).
    await page.goto(`${origin}/mod`);
    const first = queueItem(page, 'bob');
    await first.getByTestId('queue-action').selectOption('suspend_user');
    await first.getByTestId('queue-reason').selectOption('bot');
    const started = page.waitForResponse((r) => r.url().includes('/resolve'));
    await first.getByTestId('queue-resolve').click();
    expect((await started).status()).toBe(204);
    await expect(first.getByTestId('queue-awaiting')).toHaveAttribute('data-action', 'suspend_user');

    // Nothing has happened yet.
    expect(await bob.status('/me')).toBe(200);
    expect((await (await Actor.as('alice')).get<{ items: unknown[] }>('/blame')).items).toHaveLength(0);
    expect((await carol.friend('alice')).invited_removed).toBeFalsy();

    // pktwo confirms: the co-sign dialog shows the ModAction record.
    await page2.goto(`${origin}/mod`);
    const second = queueItem(page2, 'bob');
    const confirmed = page2.waitForResponse((r) => r.url().includes('/confirm'));
    await second.getByTestId('queue-confirm').click();
    const dialog = page2.getByTestId('cosign-dialog');
    await expect(dialog.getByTestId('cosign-record')).toHaveAttribute('data-kind', 'mod_action');
    await dialog.getByTestId('cosign-sign').click();
    const res = await confirmed;
    expect(res.status()).toBe(204);
    expect(Object.keys((res.request().postDataJSON() ?? {}) as object).sort()).toEqual(['assertion', 'intent']);
    await expect(page2.getByTestId('queue-empty')).toBeVisible();

    // Applied: bob is suspended, alice blamed, carol sees the mark on alice.
    expect(await bob.status('/me')).toBe(404);
    const alice = await Actor.as('alice');
    const blame = await alice.get<{ items: { action: string; invitee_handle: string }[] }>('/blame');
    expect(blame.items).toMatchObject([{ action: 'suspend_user', invitee_handle: 'bob' }]);
    const aliceForCarol = await carol.friend('alice');
    expect(aliceForCarol.invited_removed).toBe(true);
    expect((await carol.get<PersonJSON>(`/people/${aliceForCarol.ref}`)).invited_removed).toBe(true);
    // Never on alice's own view; xavier (another component) cannot even see her.
    const me = await alice.get<{ ref: string }>('/me');
    expect((await alice.get<PersonJSON>(`/people/${me.ref}`)).invited_removed).toBeFalsy();
    const xavier = await Actor.as('xavier');
    expect(await xavier.status(`/people/${aliceForCarol.ref}`)).toBe(404);
  } finally {
    await ctx2.close();
  }
});

// ---- generated images (the synthetic blocklisted pattern; never real imagery) ----

const crcTable = Array.from({ length: 256 }, (_, n) => {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c >>> 0;
});

function crc32(b: Uint8Array): number {
  let c = 0xffffffff;
  for (const x of b) c = (crcTable[(c ^ x) & 0xff] ?? 0) ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function u32(n: number): Buffer {
  const b = Buffer.alloc(4);
  b.writeUInt32BE(n >>> 0);
  return b;
}

function chunk(type: string, data: Buffer): Buffer {
  const td = Buffer.concat([Buffer.from(type, 'latin1'), data]);
  return Buffer.concat([u32(data.length), td, u32(crc32(td))]);
}

// blockedPNG is the 32×32 pattern whose SHA-256 is in testdata/scan/blocklist.txt
// (the same bytes as web/e2e/p1-w04.spec.ts: RGB, filter 0, stored deflate).
function blockedPNG(): Buffer {
  const w = 32;
  const h = 32;
  const raw = Buffer.alloc(h * (1 + 3 * w));
  let o = 0;
  for (let y = 0; y < h; y++) {
    raw[o++] = 0;
    for (let x = 0; x < w; x++) {
      raw[o++] = x * 8;
      raw[o++] = y * 8;
      raw[o++] = 0xc0;
    }
  }
  const hdr = Buffer.alloc(5);
  hdr[0] = 1;
  hdr.writeUInt16LE(raw.length, 1);
  hdr.writeUInt16LE(raw.length ^ 0xffff, 3);
  let a = 1;
  let b = 0;
  for (const x of raw) {
    a = (a + x) % 65521;
    b = (b + a) % 65521;
  }
  const z = Buffer.concat([Buffer.from([0x78, 0x01]), hdr, raw, u32(((b << 16) | a) >>> 0)]);
  const ihdr = Buffer.concat([u32(w), u32(h), Buffer.from([8, 2, 0, 0, 0])]);
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', ihdr),
    chunk('IDAT', z),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

test('a held image is queued by its match metadata only; its report needs a second moderator', async ({ page }) => {
  const w = await world();
  await w.reset();
  await w.seed('p1-w06');
  const ola = await Actor.as('ola');
  const up = await ola.req.post(`${origin}/api/media`, {
    headers: { ...sameOrigin, Origin: origin, 'X-PCC-CSRF': ola.csrf },
    multipart: { file: { name: 'pattern.png', mimeType: 'image/png', buffer: blockedPNG() }, alt: '' },
  });
  expect(up.status()).toBe(422);

  // The API answer carries no media URL and no content.
  const mo1 = await Actor.as('mo1');
  const raw = await mo1.req.get(`${origin}/api/mod/queue`, { headers: sameOrigin });
  const text = await raw.text();
  expect(text).not.toContain('/m/');
  expect(text).not.toContain('"post"');
  const items = (JSON.parse(text) as { items: ItemJSON[] }).items;
  expect(items[0]).toMatchObject({ source: 'csam', reason: 'csam', subject: { handle: 'ola' } });
  expect(Object.keys(items[0]?.csam?.match_info ?? {}).length).toBeGreaterThan(0);

  // mo1 records the report in the queue page (signed in just now).
  await page.context().clearCookies();
  await new World(page.request, origin).login('mo1');
  await page.goto(`${origin}/mod`);
  const item = page.locator('[data-testid="queue-item"][data-source="csam"]');
  await expect(item.getByTestId('queue-csam')).toBeVisible();
  await expect(item.locator('img, video, a')).toHaveCount(0);
  await item.getByTestId('queue-csam-ref').fill('CT-2026-0001');
  const started = page.waitForResponse((r) => r.url().includes('/csam-report'));
  await item.getByTestId('queue-csam-report').click();
  expect((await started).status()).toBe(204);
  await expect(item.getByTestId('queue-awaiting')).toHaveAttribute('data-action', 'csam_report');
  expect((await mo1.queue())[0]?.csam?.reported_at).toBeUndefined();

  // The same moderator cannot confirm; mo2 does.
  const own = page.waitForResponse((r) => r.url().includes('/confirm'));
  await item.getByTestId('queue-confirm').click();
  expect((await own).status()).toBe(409);
  await expect(item.getByTestId('queue-error')).toBeVisible();
  await page.context().clearCookies();
  await new World(page.request, origin).login('mo2');
  await page.goto(`${origin}/mod`);
  const done = page.waitForResponse((r) => r.url().includes('/confirm'));
  await page.locator('[data-testid="queue-item"][data-source="csam"]').getByTestId('queue-confirm').click();
  expect((await done).status()).toBe(204);
  await expect(page.getByTestId('queue-empty')).toBeVisible();
});

test('three confirmed bad accounts let two moderators quarantine exactly the subtree', async ({ page }) => {
  test.setTimeout(90_000);
  const w = await world();
  await w.reset();
  await w.seed('p1-w06');
  const carol = await Actor.as('carol');
  const mo1 = await Actor.as('mo1');
  const mo2 = await Actor.as('mo2');
  for (const kid of ['kid1', 'kid2', 'kid3']) await suspend(carol, mo1, mo2, kid);

  const kid4 = await carol.friend('kid4');
  expect(await carol.post('/reports', { target: { kind: 'person', ref: kid4.ref }, reason: 'bot' })).toBe(202);
  const item = about(await mo1.queue(), 'kid4');
  expect(item?.summary).toMatchObject({ subtree_confirmed: 3, quarantine_k: 3 });

  // mo1 starts the quarantine on the page; mo2 confirms it.
  await page.context().clearCookies();
  await new World(page.request, origin).login('mo1');
  await page.goto(`${origin}/mod`);
  const row = queueItem(page, 'kid4');
  await expect(row.getByTestId('queue-subtree')).toBeVisible();
  const started = page.waitForResponse((r) => r.url().endsWith('/api/mod/quarantine'));
  await row.getByTestId('queue-quarantine').click();
  expect((await started).status()).toBe(204);
  await expect(row.getByTestId('queue-awaiting')).toHaveAttribute('data-action', 'quarantine_subtree');

  interface BudgetJSON {
    probation?: unknown;
    paused_until?: string;
  }
  const kid4Self = await Actor.as('kid4');
  expect((await kid4Self.get<BudgetJSON>('/invites/budget')).probation).toBeUndefined();

  await page.context().clearCookies();
  await new World(page.request, origin).login('mo2');
  await page.goto(`${origin}/mod`);
  const done = page.waitForResponse((r) => r.url().includes('/confirm'));
  await queueItem(page, 'kid4').getByTestId('queue-confirm').click();
  expect((await done).status()).toBe(204);
  await expect(queueItem(page, 'kid4').getByTestId('queue-awaiting')).toHaveCount(0);

  // The subtree is on probation, root's invites are paused, the sibling is untouched.
  for (const h of ['kid4', 'grand']) {
    expect((await (await Actor.as(h)).get<BudgetJSON>('/invites/budget')).probation, h).toBeDefined();
  }
  const root = await (await Actor.as('root')).get<BudgetJSON>('/invites/budget');
  expect(root.probation).toBeUndefined();
  expect(root.paused_until).toBeDefined();
  const sib = await (await Actor.as('sib')).get<BudgetJSON>('/invites/budget');
  expect(sib.probation).toBeUndefined();
  expect(sib.paused_until).toBeUndefined();
});
