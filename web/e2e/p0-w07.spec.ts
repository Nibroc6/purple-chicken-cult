// P0-W07 end to end: the tiered feed (tiers in order under headers that come
// only from the API's markers, one session continued by "load more", threads
// as one card, the distant low-trust group expanded on request, caught up at
// the end, never the reader's own posts or another component), Latest /
// Balanced saved as the default, "N new posts above" while a session is open,
// the per-page re-check after a block, viewer-bound cursors and the restart on
// an expired session, "why", folding by keyboard, the Compact format, and seen
// posts not coming back. Selectors avoid English text so the spec runs in en
// and en-XA (post bodies are user content).
import { expect, test, type APIRequestContext, type Page, type Response } from '@playwright/test';
import { World } from './fixtures/world';

interface PostJSON {
  ref: string;
  body: string;
  tier: number;
  author: { handle: string };
}

interface ItemJSON {
  type: string;
  tier?: number;
  post?: PostJSON;
  thread?: PostJSON[];
  collapsed?: { count: number; token: string };
}

interface PageJSON {
  session: string;
  preset: string;
  items: ItemJSON[];
  next?: string;
}

const sameOrigin = { 'Sec-Fetch-Site': 'same-origin' };
const notFound = '{"error":"not_found"}\n';

function headers(origin: string, csrf: string): Record<string, string> {
  return { Origin: origin, ...sameOrigin, 'X-PCC-CSRF': csrf };
}

// setup resets the run and loads the fixture: alice's friends bob and fran
// (23 feed items in tier 1, a 3-part thread and a long post among them), carol
// two steps away (via bob only), dave (old account) and zoe (new account)
// three steps away, xavier in another component.
async function setup(request: APIRequestContext, baseURL: string | undefined): Promise<World> {
  const world = new World(request, baseURL ?? '');
  await world.reset();
  await world.seed('p0-w07');
  return world;
}

async function loginAs(page: Page, baseURL: string | undefined, handle: string): Promise<string> {
  await page.context().clearCookies();
  return new World(page.request, baseURL ?? '').login(handle);
}

const isFeedPage = (r: Response) => new URL(r.url()).pathname === '/api/feed';

// openFeed loads / and returns the first page's JSON.
async function openFeed(page: Page): Promise<PageJSON> {
  const first = page.waitForResponse(isFeedPage);
  await page.goto('/');
  const res = await first;
  expect(res.status()).toBe(200);
  return (await res.json()) as PageJSON;
}

async function loadMore(page: Page): Promise<Response> {
  const next = page.waitForResponse((r) => isFeedPage(r) && new URL(r.url()).searchParams.has('cursor'));
  await page.getByTestId('feed-more').click();
  return next;
}

const posts = (p: PageJSON): PostJSON[] => p.items.flatMap((it) => (it.post ? [it.post] : (it.thread ?? [])));

test('tiers in order under marker headers, one session across pages, caught up at the end', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  const p1 = await openFeed(page);
  expect(p1.preset).toBe('balanced');
  expect(p1.items[0]).toEqual({ type: 'tier_start', tier: 1 });
  expect(p1.next).toBeTruthy();
  expect(p1.items.filter((it) => it.type !== 'tier_start')).toHaveLength(20);

  // One seen target per card: 20 cards, though the thread card shows 3 posts.
  await expect(page.locator('[data-seen-ref]')).toHaveCount(20);
  await expect(page.getByTestId('integrity-tier-header')).toHaveCount(1);
  const thread = page.getByTestId('feed-thread');
  await expect(thread).toHaveAttribute('data-parts', '3');
  await expect(thread.locator('[data-seen-ref]')).toHaveCount(1);
  await expect(thread.locator('[data-part="context"]').first()).toBeHidden();
  await thread.getByTestId('feed-thread-toggle').click();
  await expect(thread.getByText('Thread part one')).toBeVisible();

  const p2 = (await (await loadMore(page)).json()) as PageJSON;
  expect(p2.session).toBe(p1.session);
  // The reply (6 h behind under Balanced) came on page 2 with its context row.
  await expect(page.locator('[data-testid="feed-post"]', { hasText: 'Bob replies to carol' }).getByTestId('feed-context')).toContainText(
    'Carol says hi',
  );
  // The rest of tier 1, then carol (tier 2), then the new account's group
  // (its newest post ranks first) and dave (tier 3), then caught up.
  expect(p2.items.map((it) => (it.type === 'thread' ? 'post' : it.type)).slice(-9)).toEqual([
    'tier_end',
    'tier_start',
    'post',
    'tier_end',
    'tier_start',
    'collapsed',
    'post',
    'tier_end',
    'caught_up',
  ]);
  expect(p2.items.at(-1)).toEqual({ type: 'caught_up' });
  expect(p2.next).toBeUndefined();
  // Headers come only from the markers: one per tier_start / tier_end / caught_up.
  const markers = [...p1.items, ...p2.items].filter((it) => ['tier_start', 'tier_end', 'caught_up'].includes(it.type)).length;
  await expect(page.getByTestId('integrity-tier-header')).toHaveCount(markers);
  await expect(page.getByTestId('feed-tier')).toHaveCount(3);
  expect(await page.getByTestId('feed-tier').evaluateAll((els) => els.map((e) => e.getAttribute('data-tier')))).toEqual(['1', '2', '3']);
  await expect(page.getByTestId('feed-more')).toHaveCount(0);

  // Every tier-1 post before every tier-2 one; the reader's own posts and another component never appear.
  const all = [...posts(p1), ...posts(p2)];
  const tiers = all.map((p) => p.tier);
  expect([...tiers].sort((a, b) => a - b)).toEqual(tiers);
  expect(all.map((p) => p.body)).not.toContain("Alice's own post");
  await expect(page.getByText('Xavier is never shown')).toHaveCount(0);
  await expect(page.getByText("Alice's own post")).toHaveCount(0);
  const carol = page.locator('[data-testid="feed-post"][data-tier="2"]', {
    hasText: 'Carol says hi',
  });
  await expect(carol.getByTestId('integrity-chain')).toContainText('@bob');
  await expect(carol).toHaveAttribute('data-tier', '2');

  // The new account three steps away is folded into a group until asked for.
  await expect(page.getByText('Dave is three steps away')).toBeVisible();
  await expect(page.getByText('Zoe posts again')).toHaveCount(0);
  const group = page.getByTestId('feed-collapsed');
  await expect(group).toHaveAttribute('data-count', '2');
  const expand = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/feed/expand');
  await group.getByTestId('feed-expand').click();
  expect((await expand).status()).toBe(200);
  await expect(page.getByText('Zoe posts again')).toBeVisible();
  await expect(page.getByTestId('feed-collapsed')).toHaveCount(0);
});

test('Latest is newest first and becomes the saved order', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  await openFeed(page);
  await expect(page.getByTestId('feed-preset-balanced')).toHaveAttribute('aria-pressed', 'true');
  const latest = page.waitForResponse((r) => isFeedPage(r) && new URL(r.url()).searchParams.get('preset') === 'latest');
  const saved = page.waitForResponse((r) => r.request().method() === 'PATCH' && r.url().endsWith('/api/me/settings'));
  await page.getByTestId('feed-preset-latest').click();
  const body = (await (await latest).json()) as PageJSON;
  expect((await saved).status()).toBe(200);
  expect(
    posts(body)
      .map((p) => p.body)
      .slice(0, 5),
  ).toEqual([
    'Bob replies to carol',
    'Thread part one',
    'Thread part two',
    'Thread part three',
    'Line 1 of a long post\nLine 2 of a long post\nLine 3 of a long post\nLine 4 of a long post\nLine 5 of a long post\nLine 6 of a long post\nLine 7 of a long post\nLine 8 of a long post\nLine 9 of a long post',
  ]);
  await expect(page.getByTestId('feed-preset-latest')).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('[data-seen-ref]').first()).toContainText('Bob replies to carol');

  // The next visit starts in the saved order without asking for it.
  const again = await openFeed(page);
  expect(again.preset).toBe('latest');
  await expect(page.getByTestId('feed-preset-latest')).toHaveAttribute('aria-pressed', 'true');
});

test('new posts in reached tiers are counted above, and shown after starting over', async ({ page, request, baseURL }) => {
  const origin = baseURL ?? '';
  const world = await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  await page.clock.install();
  await openFeed(page);
  await expect(page.getByTestId('feed-new-above')).toHaveCount(0);

  // bob publishes after the snapshot (the server clock moves on first).
  await world.advance('1m');
  const csrf = await world.login('bob');
  const res = await request.post('/api/posts', {
    headers: headers(origin, csrf),
    data: { body: 'Fresh from bob', lang: 'en' },
  });
  expect(res.status()).toBe(201);
  await expect(page.getByText('Fresh from bob')).toHaveCount(0);

  const poll = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/feed/new');
  await page.clock.fastForward('01:05');
  expect((await poll).status()).toBe(200);
  const banner = page.getByTestId('feed-new-above');
  await expect(banner).toHaveAttribute('data-count', '1');
  const restart = page.waitForResponse((r) => isFeedPage(r) && !new URL(r.url()).searchParams.has('cursor'));
  await banner.click();
  await restart;
  await expect(page.getByTestId('feed-page')).toHaveAttribute('data-session-gen', '1');
  await expect(page.getByText('Fresh from bob')).toBeVisible();
  await expect(page.getByTestId('feed-new-above')).toHaveCount(0);
});

test('after a block mid-session the next page leaves the blocked person out', async ({ page, request, baseURL }) => {
  const origin = baseURL ?? '';
  await setup(request, baseURL);
  const csrf = await loginAs(page, baseURL, 'alice');
  const p1 = await openFeed(page);
  expect(posts(p1).some((p) => p.author.handle === 'bob')).toBe(true);
  const friends = (await (await page.request.get('/api/friends', { headers: sameOrigin })).json()) as {
    items: { ref: string; handle: string }[];
  };
  const bob = friends.items.find((f) => f.handle === 'bob');
  expect(bob).toBeDefined();
  const block = await page.request.put(`/api/people/${bob?.ref ?? ''}/block`, {
    headers: headers(origin, csrf),
  });
  expect(block.status()).toBe(204);

  const p2 = (await (await loadMore(page)).json()) as PageJSON;
  expect(posts(p2).filter((p) => p.author.handle === 'bob')).toEqual([]);
  // carol and everyone behind her were reachable through bob only.
  expect(p2.items.some((it) => it.type === 'tier_start')).toBe(false);
  expect(p2.items.at(-1)).toEqual({ type: 'caught_up' });
  await expect(page.getByText('Carol says hi')).toHaveCount(0);
});

test('cursors belong to their viewer; an expired session starts over', async ({ page, request, baseURL, playwright }) => {
  const world = await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  const p1 = await openFeed(page);
  const cursor = p1.next ?? '';

  // Another signed-in viewer learns nothing from alice's cursor or session.
  const bobReq = await playwright.request.newContext({ baseURL: baseURL ?? '' });
  await new World(bobReq, baseURL ?? '').login('bob');
  for (const path of [`/api/feed?cursor=${cursor}`, `/api/feed/new?session=${p1.session}`, '/api/feed?cursor=garbage']) {
    const res = await bobReq.get(path, { headers: sameOrigin });
    expect(`${String(res.status())} ${await res.text()}`).toBe(`404 ${notFound}`);
  }
  await bobReq.dispose();
  // Her own session expires after 30 unused minutes: 409, and the page starts over.
  await world.advance('31m');
  const own = await page.request.get(`/api/feed?cursor=${cursor}`, {
    headers: sameOrigin,
  });
  expect(own.status()).toBe(409);
  expect(((await own.json()) as { error: string }).error).toBe('feed_session_expired');
  const restart = page.waitForResponse((r) => isFeedPage(r) && !new URL(r.url()).searchParams.has('cursor'));
  await page.getByTestId('feed-more').click();
  expect((await restart).status()).toBe(200);
  await expect(page.getByTestId('feed-page')).toHaveAttribute('data-session-gen', '1');
  await expect(page.locator('[data-seen-ref]')).toHaveCount(20);
});

test('why, folding by keyboard and the Compact format', async ({ page, request, baseURL }) => {
  const origin = baseURL ?? '';
  await setup(request, baseURL);
  const csrf = await loginAs(page, baseURL, 'alice');
  await openFeed(page);
  await loadMore(page);

  const ref = await page.locator('[data-testid="feed-post"][data-tier="2"]', { hasText: 'Carol says hi' }).getAttribute('data-ref');
  const why = (await (
    await page.request.get(`/api/posts/${ref ?? ''}/why`, {
      headers: sameOrigin,
    })
  ).json()) as {
    tier: number;
    preset: string;
    via: { first_hop?: { handle: string } };
    reasons: string[];
  };
  expect(why).toMatchObject({
    tier: 2,
    preset: 'balanced',
    via: { first_hop: { handle: 'bob' } },
  });
  expect(why.reasons).toContain('network_likes');
  const garbage = await page.request.get('/api/posts/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA/why', { headers: sameOrigin });
  expect(`${String(garbage.status())} ${await garbage.text()}`).toBe(`404 ${notFound}`);

  const long = page.locator('[data-testid="feed-post"]', {
    hasText: 'Line 1 of a long post',
  });
  const toggle = long.getByTestId('feed-fold-toggle');
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await toggle.focus();
  await page.keyboard.press('Enter');
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  await expect(long.getByText('Line 9 of a long post')).toBeVisible();

  const patch = await page.request.patch('/api/me/settings', {
    headers: headers(origin, csrf),
    data: { ui_prefs: { format: 'compact' } },
  });
  expect(patch.status()).toBe(200);
  await openFeed(page);
  await expect(page.getByTestId('feed-layout')).toHaveAttribute('data-format', 'compact');
  await expect(page.getByTestId('integrity-tier-header').first()).toBeVisible();
  const card = page.locator('[data-seen-ref]').first();
  for (const id of ['integrity-chain', 'integrity-why', 'integrity-audience', 'integrity-report']) {
    await expect(card.getByTestId(id)).toBeVisible();
  }
  await expect(card.getByTestId('integrity-edited')).toBeAttached();
  await expect(page.locator('[data-seen-ref]')).toHaveCount(20);
});

test('cards seen for a second do not come back in the next session', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  await openFeed(page);
  await expect(page.locator('[data-seen-ref]')).toHaveCount(20);
  const cards = await page.locator('[data-seen-ref]').evaluateAll((els) => els.map((e) => e.getAttribute('data-seen-ref') ?? ''));
  const recent = async () =>
    ((await (await page.request.get('/api/seen/recent', { headers: sameOrigin })).json()) as { items: PostJSON[] }).items.map((p) => p.ref);
  await expect
    .poll(async () => (await recent()).length, {
      timeout: 15_000,
      intervals: [500],
    })
    .toBeGreaterThan(0);
  const seen = await recent();
  // Only cards' own posts are ever recorded (no context rows, no earlier thread parts).
  for (const ref of seen) expect(cards).toContain(ref);

  const next = await openFeed(page);
  const refs = posts(next).map((p) => p.ref);
  for (const ref of seen) expect(refs).not.toContain(ref);
});
