// P0-W10 end to end: a friend request reaches the nav badge live over SSE and
// the notifications page (marking it read clears the badge); declining tells
// the requester nothing; replies to a post batch into one row with a count,
// a mention gets its own, muting a post stops its replies; nothing at all from
// someone the recipient blocked; the event stream closes within a second of
// logout and cannot reopen; the "why" panel opens from a feed card, loads once
// and closes with Escape; integrity elements carry no links. Selectors avoid
// English text so the spec runs in en and en-XA (post bodies are user content).
import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { World } from './fixtures/world';

interface PostJSON {
  ref: string;
  body: string;
  author: { ref: string; handle: string };
}
interface FeedJSON {
  items: { post?: PostJSON }[];
}
interface NotificationJSON {
  id: string;
  kind: string;
  count: number;
  person?: { ref: string; handle: string };
  post?: string;
  read: boolean;
}
interface ListJSON {
  items: NotificationJSON[];
  unread: number;
}

const sameOrigin = { 'Sec-Fetch-Site': 'same-origin' };

// setup: alice – bob – carol – dave, xavier in another component; alice posted a1.
async function setup(request: APIRequestContext, baseURL: string | undefined): Promise<void> {
  const world = new World(request, baseURL ?? '');
  await world.reset();
  await world.seed('p0-w10');
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

  private headers() {
    return { Origin: this.origin, ...sameOrigin, 'X-PCC-CSRF': this.csrf };
  }

  async get<T>(path: string): Promise<T> {
    const res = await this.req.get(`/api${path}`, { headers: sameOrigin });
    expect(res.status(), path).toBe(200);
    return (await res.json()) as T;
  }

  async send(method: 'POST' | 'PUT' | 'DELETE', path: string, data?: unknown): Promise<number> {
    const res = await this.req.fetch(`/api${path}`, { method, headers: this.headers(), ...(data === undefined ? {} : { data }) });
    return res.status();
  }

  // feedPost finds a post by its author's handle in the actor's feed.
  async feedPost(author: string): Promise<PostJSON> {
    const feed = await this.get<FeedJSON>('/feed?preset=latest');
    const p = feed.items.flatMap((it) => (it.post ? [it.post] : [])).find((x) => x.author.handle === author);
    if (!p) throw new Error(`no post by ${author}`);
    return p;
  }

  async reply(parent: string, body: string): Promise<void> {
    expect(await this.send('POST', '/posts', { body, lang: 'en', parents: [{ ref: parent, kind: 'ref' }] })).toBe(201);
  }

  async notifications(): Promise<ListJSON> {
    return this.get<ListJSON>('/notifications');
  }
}

async function loginPage(page: Page, baseURL: string | undefined, handle: string): Promise<Actor> {
  await page.context().clearCookies();
  return Actor.login(page.request, baseURL ?? '', handle);
}

async function actor(playwright: { request: { newContext(o: object): Promise<APIRequestContext> } }, baseURL: string | undefined, handle: string) {
  const req = await playwright.request.newContext({ baseURL });
  return Actor.login(req, baseURL ?? '', handle);
}

test('a friend request reaches the badge live; declining tells the requester nothing', async ({ page, playwright, request, baseURL }) => {
  await setup(request, baseURL);
  const alice = await loginPage(page, baseURL, 'alice');
  await page.goto('/');
  await expect(page.getByTestId('feed-page')).toBeVisible();
  await expect(page.getByTestId('notifications-badge')).toHaveCount(0);

  const carol = await actor(playwright, baseURL, 'carol');
  const aliceRef = (await carol.feedPost('alice')).author.ref;
  expect(await carol.send('POST', `/people/${aliceRef}/request`)).toBe(202);

  // Live over SSE, no reload.
  await expect(page.getByTestId('notifications-badge')).toHaveAttribute('data-count', '1', { timeout: 5000 });

  await page.getByRole('link', { name: /./ }).filter({ has: page.getByTestId('notifications-badge') }).click();
  await expect(page).toHaveURL(/\/notifications$/);
  const item = page.getByTestId('notification');
  await expect(item).toHaveCount(1);
  await expect(item).toHaveAttribute('data-kind', 'friend_request');
  await expect(item).toHaveAttribute('data-read', 'false');
  await expect(item.getByTestId('notification-person')).toContainText('@carol');
  await expect(page.getByTestId('notifications-badge')).toHaveCount(0); // marked read
  expect((await alice.notifications()).unread).toBe(0);

  // Alice declines (the ref comes from her own notification).
  const list = await alice.notifications();
  const carolRef = list.items[0]?.person?.ref ?? '';
  expect(await alice.send('POST', `/friends/requests/${carolRef}/decline`)).toBe(204);
  const carolsList = await carol.notifications();
  expect(carolsList).toEqual({ items: [], unread: 0 });
  // Asking again still says nothing to anyone new.
  expect(await carol.send('POST', `/people/${aliceRef}/request`)).toBe(202);
  expect((await carol.notifications()).items).toHaveLength(0);
  expect((await alice.notifications()).items).toHaveLength(1);
  await carol.req.dispose();
});

test('replies batch into one row with a count, a mention stands alone, muting stops replies', async ({ page, playwright, request, baseURL }) => {
  await setup(request, baseURL);
  const alice = await loginPage(page, baseURL, 'alice');
  const bob = await actor(playwright, baseURL, 'bob');
  const carol = await actor(playwright, baseURL, 'carol');
  const a1 = (await bob.feedPost('alice')).ref;
  await bob.reply(a1, 'First reply from bob');
  await bob.reply(a1, 'Second reply from bob');
  const a1c = (await carol.feedPost('alice')).ref;
  await carol.reply(a1c, 'A reply from carol');
  expect(await carol.send('POST', '/posts', { body: 'Thinking of @alice today', lang: 'en' })).toBe(201);

  let list = await alice.notifications();
  expect(list.unread).toBe(2);
  const referenced = list.items.filter((n) => n.kind === 'post_referenced');
  expect(referenced).toHaveLength(1);
  expect(referenced[0]).toMatchObject({ count: 3, person: { handle: 'carol' } });
  expect(list.items.filter((n) => n.kind === 'mention')).toHaveLength(1);

  await page.goto('/notifications');
  const row = page.locator('[data-testid="notification"][data-kind="post_referenced"]');
  await expect(row).toHaveCount(1);
  await expect(row.getByTestId('notification-post')).toHaveAttribute('href', `/p/${referenced[0]?.post ?? ''}`);
  await row.getByTestId('notification-mute').click();
  await expect(row.getByTestId('notification-mute')).toHaveAttribute('data-muted', 'true');
  await expect.poll(async () => (await alice.notifications()).unread).toBe(0);

  // Dave is three steps from alice, so only carol can still reply within reach: muted, nothing new.
  await carol.reply(a1c, 'Another reply from carol');
  list = await alice.notifications();
  expect(list.unread).toBe(0);
  expect(list.items.filter((n) => n.kind === 'post_referenced' && !n.read)).toHaveLength(0);
  await bob.req.dispose();
  await carol.req.dispose();
});

test('nothing at all from someone the recipient blocked', async ({ page, playwright, request, baseURL }) => {
  await setup(request, baseURL);
  const alice = await loginPage(page, baseURL, 'alice');
  const carol = await actor(playwright, baseURL, 'carol');
  const carolRef = (await alice.feedPost('carol')).author.ref;
  expect(await alice.send('PUT', `/people/${carolRef}/block`)).toBe(204);
  // Blocks are one-directional: carol still sees alice and writes to her.
  const a1 = (await carol.feedPost('alice')).ref;
  await carol.reply(a1, 'Replying anyway');
  expect(await carol.send('POST', '/posts', { body: 'Hello @alice', lang: 'en' })).toBe(201);
  expect(await alice.notifications()).toEqual({ items: [], unread: 0 });
  await page.goto('/notifications');
  await expect(page.getByTestId('notifications-empty')).toBeVisible();
  await carol.req.dispose();
});

test('the event stream closes within a second of logout and cannot reopen', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  const alice = await loginPage(page, baseURL, 'alice');
  await page.goto('/notifications');
  await expect(page.getByTestId('notifications-page')).toBeVisible();
  // A second, observable stream in the same tab.
  await page.evaluate(() => {
    const w = window as unknown as { pccEvents: string[]; pccClosedAt?: number };
    w.pccEvents = [];
    const es = new EventSource('/api/events');
    es.addEventListener('hello', (e) => w.pccEvents.push(`hello:${(e as MessageEvent<string>).data}`));
    es.onerror = () => {
      w.pccClosedAt ??= Date.now();
      es.close();
    };
  });
  await expect.poll(() => page.evaluate(() => (window as unknown as { pccEvents: string[] }).pccEvents)).toEqual(['hello:{"unread":0}']);
  const before = await page.evaluate(() => Date.now());
  expect(await alice.send('POST', '/auth/logout')).toBe(204);
  await expect
    .poll(() => page.evaluate(() => (window as unknown as { pccClosedAt?: number }).pccClosedAt), { timeout: 2000 })
    .toBeGreaterThan(0);
  const closedAt = await page.evaluate(() => (window as unknown as { pccClosedAt: number }).pccClosedAt);
  expect(closedAt - before).toBeLessThan(1000);
  const again = await page.request.get('/api/events', { headers: sameOrigin });
  expect(again.status()).toBe(404);
});

test('"why" opens from a feed card, loads once and closes with Escape; integrity elements carry no links', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  await loginPage(page, baseURL, 'alice');
  const whyCalls: string[] = [];
  page.on('request', (r) => {
    if (/\/api\/posts\/[^/]+\/why$/.test(new URL(r.url()).pathname)) whyCalls.push(r.url());
  });
  await page.goto('/');
  const card = page.getByTestId('feed-post').first();
  await expect(card).toBeVisible();
  for (const id of ['integrity-chain', 'integrity-inviter-label', 'integrity-audience', 'integrity-report']) {
    await expect(card.getByTestId(id)).toBeVisible();
    await expect(card.getByTestId(id).locator('a')).toHaveCount(0);
  }
  const why = card.getByTestId('integrity-why');
  await expect(why).toHaveAttribute('aria-expanded', 'false');
  await why.click();
  const panel = card.getByTestId('integrity-why-panel');
  await expect(panel.getByTestId('integrity-why-body')).toBeVisible();
  await expect(panel.getByTestId('integrity-why-tier')).not.toBeEmpty();
  await expect(why).toHaveAttribute('aria-expanded', 'true');
  await page.keyboard.press('Escape');
  await expect(panel).toHaveCount(0);
  await expect(why).toBeFocused();
  await why.click();
  await expect(card.getByTestId('integrity-why-body')).toBeVisible();
  expect(whyCalls).toHaveLength(1);
});
