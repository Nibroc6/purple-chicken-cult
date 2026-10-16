// P0-W08 end to end: the family tree (/p/:ref) — rows around the focal post
// under section headers (parents above with the primary parent's siblings in
// the focal row, replies below, ranked by distance tier then the reader's
// preset), "+N more" after five with the rest of the row as a ranked list and
// further pages, tier hairlines, earlier / later rows, a diamond shown once
// ("shown above") with a chip that refocuses, no trace of hidden parents or
// children, re-centering by keyboard buttons and by swiping (the rows below
// reload, the rows above stay), folding at 10 lines by keyboard, viewer-bound
// snapshots with a reopen on expiry, and the Compact outline. Selectors avoid
// English text so the spec runs in en and en-XA (post bodies are user content).
import { expect, test, type APIRequestContext, type Locator, type Page, type Response } from '@playwright/test';
import { World } from './fixtures/world';

interface PostJSON {
  ref: string;
  body: string;
  tier: number;
  reply_count: number;
  author: { handle: string };
}

interface ItemJSON {
  post: PostJSON;
  tier_divider?: boolean;
  shown_above?: boolean;
  also_replies_to?: { handle: string }[];
  also_parents?: string[];
}

interface RowJSON {
  gen: number;
  label: string;
  center: string;
  items: ItemJSON[];
  more: number;
  cursor?: string;
}

interface TreeJSON {
  snapshot: string;
  focal: string;
  rows: RowJSON[];
  has_earlier: boolean;
  has_later: boolean;
}

interface FeedJSON {
  items: { post?: PostJSON; thread?: PostJSON[] }[];
  next?: string;
}

const sameOrigin = { 'Sec-Fetch-Site': 'same-origin' };
const notFound = '{"error":"not_found"}\n';

// setup resets the run and loads the fixture: alice (Latest) with friends bob
// and fran, carol two steps away, dave three. bob's focal post replies to
// bob's parent (under carol's grandparent and root) and to carol's second
// parent; it has 12 visible replies (seven by friends, the newest of them a
// 12-paragraph post by fran, then five by carol, one a diamond also replying to
// fran's post) and one by dave for his friends only. carol replies to dave's
// friends-only post; xavier is in another component.
async function setup(request: APIRequestContext, baseURL: string | undefined): Promise<World> {
  const world = new World(request, baseURL ?? '');
  await world.reset();
  await world.seed('p0-w08');
  return world;
}

async function loginAs(page: Page, baseURL: string | undefined, handle: string): Promise<string> {
  await page.context().clearCookies();
  return new World(page.request, baseURL ?? '').login(handle);
}

// refOf finds alice's ref of a post by its body, paging through her feed.
async function refOf(page: Page, body: string): Promise<string> {
  let cursor: string | undefined;
  for (let i = 0; i < 5; i++) {
    const res = await page.request.get(cursor ? `/api/feed?cursor=${cursor}` : '/api/feed', { headers: sameOrigin });
    expect(res.status()).toBe(200);
    const page1 = (await res.json()) as FeedJSON;
    for (const it of page1.items) {
      for (const p of it.post ? [it.post] : (it.thread ?? [])) if (p.body === body) return p.ref;
    }
    if (!page1.next) break;
    cursor = page1.next;
  }
  throw new Error(`no post ${body}`);
}

const isOpen = (r: Response) => /^\/api\/tree\/[^/]+$/.test(new URL(r.url()).pathname);
const isRow = (r: Response) => /^\/api\/tree\/s\/[^/]+\/row$/.test(new URL(r.url()).pathname);
const isMore = (r: Response) => /^\/api\/tree\/s\/[^/]+\/more$/.test(new URL(r.url()).pathname);

// openTree loads /p/<ref> and returns the tree's JSON.
async function openTree(page: Page, ref: string): Promise<TreeJSON> {
  const opened = page.waitForResponse(isOpen);
  await page.goto(`/p/${ref}`);
  const res = await opened;
  expect(res.status()).toBe(200);
  await expect(page.getByTestId('tree-layout')).toBeVisible();
  return (await res.json()) as TreeJSON;
}

const rowOf = (page: Page, gen: number): Locator => page.locator(`[data-testid="tree-row"][data-gen="${String(gen)}"]`);
const bodies = (r: RowJSON | undefined) => (r?.items ?? []).map((it) => (it.shown_above ? `^${it.post.author.handle}` : it.post.body));
const longBody = Array.from({ length: 12 }, (_, i) => `Long reply line ${String(i + 1)}`).join('\n\n');

test('rows around the focal post, ranked by tier, under section headers', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  const focal = await refOf(page, 'The focal post');
  const tv = await openTree(page, focal);

  expect(tv.focal).toBe(focal);
  expect(tv.rows.map((r) => [r.gen, r.label])).toEqual([
    [-2, 'earlier'],
    [-1, 'replying_to'],
    [0, 'this_post'],
    [1, 'replies'],
    [2, 'replies_to_replies'],
  ]);
  expect(bodies(tv.rows[0])).toEqual(['Grandparent by carol']);
  // Parents: bob's (a friend) before carol's (two steps away), a hairline between.
  expect(bodies(tv.rows[1])).toEqual(['Parent by bob', 'Second parent by carol']);
  expect(tv.rows[1]?.items.map((it) => it.tier_divider ?? false)).toEqual([false, true]);
  // The focal row: the focal post first, then its siblings under the primary parent.
  expect(bodies(tv.rows[2])).toEqual(['The focal post', 'Sibling by fran']);
  // Replies: friends first (Latest: newest first), then carol's; ten per page.
  expect(bodies(tv.rows[3])).toEqual([
    longBody,
    'Reply 08 by fran',
    'Reply 07 by bob',
    'Reply 06 by fran',
    'Reply 05 by bob',
    'Reply 04 by fran',
    'Reply 03 by bob',
    'Diamond by carol',
    'Carol reply 13',
    'Carol reply 12',
  ]);
  expect(tv.rows[3]?.more).toBe(2);
  // The replies to the first reply: the diamond already shown above is a stub.
  expect(bodies(tv.rows[4])).toEqual(['Grandchild by bob', '^carol']);
  expect(tv.has_earlier && tv.has_later).toBe(true);
  // Visible links only: dave's friends-only reply leaves no trace, not even in a count.
  const all = JSON.stringify(tv);
  expect(all).not.toContain('Dave for his friends only');
  expect(tv.rows[2]?.items[0]?.post.reply_count).toBe(12);

  // The page: one section header per row, the focal row centered on the focal post.
  await expect(page.getByTestId('tree-row-header')).toHaveCount(5);
  await expect(page.getByTestId('tree-row')).toHaveCount(5);
  await expect(rowOf(page, 0).locator('[data-centered="true"]')).toHaveAttribute('data-ref', focal);
  await expect(rowOf(page, 0).locator('[data-centered="true"] .post-focal')).toBeVisible();
  await expect(rowOf(page, -1).getByTestId('tree-tier-divider')).toHaveCount(1);
  await expect(page.getByText('Xavier is never shown')).toHaveCount(0);
  for (const id of ['integrity-chain', 'integrity-inviter-label', 'integrity-why', 'integrity-audience', 'integrity-report']) {
    await expect(rowOf(page, 0).locator('[data-centered="true"]').getByTestId(id)).toBeVisible();
  }

  // "+N more" after five: N counts every visible reply beyond them.
  const replies = rowOf(page, 1);
  await expect(replies.getByTestId('tree-track').getByTestId('tree-item')).toHaveCount(5);
  const chip = replies.getByTestId('tree-more');
  await expect(chip).toHaveAttribute('data-count', '7');
  await chip.click();
  const list = replies.getByTestId('tree-more-list');
  await expect(list.getByTestId('tree-item')).toHaveCount(5);
  await expect(list.getByTestId('tree-tier-divider')).toHaveCount(1);
  const more = page.waitForResponse(isMore);
  await list.getByTestId('tree-load-more').click();
  const page2 = (await (await more).json()) as RowJSON;
  expect(bodies(page2)).toEqual(['Carol reply 11', 'Carol reply 10']);
  expect(page2.more).toBe(0);
  await expect(list.getByTestId('tree-item')).toHaveCount(7);
  await expect(list.getByTestId('tree-load-more')).toHaveCount(0);

  // Earlier rows load at the top edge, until there are none.
  const earlier = page.waitForResponse(isRow);
  await page.getByTestId('tree-earlier').click();
  const up = (await (await earlier).json()) as RowJSON;
  expect([up.gen, up.label, bodies(up)]).toEqual([-3, 'generations_up', ['Root by carol']]);
  await expect(page.getByTestId('tree-row')).toHaveCount(6);
});

test('a diamond shows once, chips refocus, hidden parents look like roots', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  const focal = await refOf(page, 'The focal post');
  await openTree(page, focal);

  const stub = rowOf(page, 2).getByTestId('tree-shown-above');
  await expect(stub).toHaveCount(1);
  await expect(rowOf(page, 2).getByText('Diamond by carol')).toHaveCount(0);
  // The diamond in the replies row names its other parent's author; the chip refocuses on it.
  const replies = rowOf(page, 1);
  await replies.getByTestId('tree-more').click();
  const diamond = replies.locator('[data-testid="tree-item"]', { hasText: 'Diamond by carol' });
  const chip = diamond.getByTestId('tree-also');
  await expect(chip).toContainText('fran');
  const reopened = page.waitForResponse(isOpen);
  await chip.click();
  const tv = (await (await reopened).json()) as TreeJSON;
  expect(bodies(tv.rows.find((r) => r.gen === 0))[0]).toBe(longBody);
  await expect(page).toHaveURL(new RegExp(`/p/${tv.focal}$`));

  // carol's reply to dave's friends-only post: no "Replying to", nothing earlier.
  const under = await refOf(page, 'Carol under a hidden parent');
  const root = await openTree(page, under);
  expect(root.rows.map((r) => r.gen)).toEqual([0]);
  expect(root.has_earlier || root.has_later).toBe(false);
  await expect(page.getByTestId('tree-row')).toHaveCount(1);
  await expect(page.getByTestId('tree-earlier')).toHaveCount(0);
  await expect(page.getByText("Dave's secret")).toHaveCount(0);
});

test('re-centering by keyboard and by swiping reloads the rows below, the rows above stay', async ({ page, request, baseURL }) => {
  await page.setViewportSize({ width: 420, height: 900 });
  await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  const focal = await refOf(page, 'The focal post');
  const tv = await openTree(page, focal);
  const replies = rowOf(page, 1);

  // Keyboard: the next button centers the second reply; its row below has nothing.
  const next = replies.getByTestId('tree-next');
  await next.focus();
  const row = page.waitForResponse(isRow);
  await page.keyboard.press('Enter');
  const r = await row;
  const q = new URL(r.url()).searchParams;
  expect([q.get('gen'), q.get('center')]).toEqual(['2', tv.rows[3]?.items[1]?.post.ref]);
  expect(((await r.json()) as RowJSON).items).toEqual([]);
  await expect(page.getByTestId('tree-row')).toHaveCount(4);
  await expect(replies.locator('[data-centered="true"]')).toHaveAttribute('data-ref', tv.rows[3]?.items[1]?.post.ref ?? '');
  await expect(rowOf(page, -2)).toBeVisible();
  await expect(rowOf(page, -1)).toBeVisible();

  // Back to the first reply: its replies return.
  const back = page.waitForResponse(isRow);
  await replies.getByTestId('tree-prev').click();
  expect(((await (await back).json()) as RowJSON).items.length).toBe(2);
  await expect(rowOf(page, 2)).toBeVisible();

  // Swipe: scroll the replies row so the third reply snaps to the middle.
  const third = tv.rows[3]?.items[2]?.post.ref ?? '';
  const swiped = page.waitForResponse((res) => isRow(res) && new URL(res.url()).searchParams.get('center') === third);
  await replies.getByTestId('tree-track').evaluate((el, ref) => {
    const slot = el.querySelector<HTMLElement>(`[data-slot-ref="${ref}"]`);
    if (slot) el.scrollTo({ left: slot.offsetLeft - (el.clientWidth - slot.clientWidth) / 2, behavior: 'instant' });
  }, third);
  expect((await swiped).status()).toBe(200);
  await expect(replies.locator('[data-centered="true"]')).toHaveAttribute('data-ref', third);

  // The focal row re-centers on the other parent: its replies are listed instead.
  const other = page.waitForResponse(isRow);
  await rowOf(page, -1).getByTestId('tree-next').click();
  const r0 = (await (await other).json()) as RowJSON;
  expect([r0.gen, bodies(r0)]).toEqual([0, ['The focal post']]);
  await expect(rowOf(page, 0).getByTestId('tree-item')).toHaveCount(1);
  await expect(rowOf(page, 1)).toBeVisible();
});

test('posts longer than 10 lines fold in the tree and expand by keyboard', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  await openTree(page, await refOf(page, 'The focal post'));
  const long = rowOf(page, 1).locator('[data-testid="tree-item"]', { hasText: 'Long reply line 12' });
  const toggle = long.getByTestId('feed-fold-toggle');
  const body = long.locator('.fold-body');
  const clipped = () => body.evaluate((el) => el.scrollHeight > el.clientHeight + 1);
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await expect(body).toHaveClass(/fold-tree/);
  expect(await clipped()).toBe(true);
  await toggle.focus();
  await page.keyboard.press('Enter');
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  await expect(body).not.toHaveClass(/fold-clamp/);
  expect(await clipped()).toBe(false);
  await expect(long.getByText(/Long reply line 12/)).toBeVisible();
  // The focal post never folds; short replies have nothing to unfold.
  await expect(rowOf(page, 0).getByTestId('feed-fold-toggle')).toHaveCount(0);
  await expect(rowOf(page, 1).getByTestId('feed-fold-toggle')).toHaveCount(1);
});

test('snapshots belong to their viewer; an expired one reopens the tree', async ({ page, request, baseURL, playwright }) => {
  const world = await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  const focal = await refOf(page, 'The focal post');
  const tv = await openTree(page, focal);
  const replies = tv.rows.find((r) => r.gen === 1);

  // bob sees every post involved, yet alice's snapshot and cursor answer him the uniform 404.
  const bobReq = await playwright.request.newContext({ baseURL: baseURL ?? '' });
  await new World(bobReq, baseURL ?? '').login('bob');
  // alice's post ref is not bob's.
  expect(await (await bobReq.get(`/api/tree/${focal}`, { headers: sameOrigin })).text()).toBe(notFound);
  for (const path of [
    `/api/tree/s/${tv.snapshot}/row?gen=1&center=${focal}`,
    `/api/tree/s/${tv.snapshot}/more?gen=1&center=${focal}&cursor=${replies?.cursor ?? ''}`,
    `/api/tree/s/garbage/row?gen=1&center=${focal}`,
  ]) {
    const res = await bobReq.get(path, { headers: sameOrigin });
    expect(`${String(res.status())} ${await res.text()}`).toBe(`404 ${notFound}`);
  }
  await bobReq.dispose();
  // A post in another component or a garbage ref: the same 404.
  for (const ref of ['AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA', 'x']) {
    const res = await page.request.get(`/api/tree/${ref}`, { headers: sameOrigin });
    expect(`${String(res.status())} ${await res.text()}`).toBe(`404 ${notFound}`);
  }

  // Her own snapshot expires after 30 unused minutes: 409, and the page reopens the tree.
  await world.advance('31m');
  const own = await page.request.get(`/api/tree/s/${tv.snapshot}/row?gen=1&center=${focal}`, { headers: sameOrigin });
  expect(own.status()).toBe(409);
  expect(((await own.json()) as { error: string }).error).toBe('tree_snapshot_expired');
  const reopened = page.waitForResponse(isOpen);
  await rowOf(page, 1).getByTestId('tree-next').click();
  expect((await reopened).status()).toBe(200);
  await expect(page.getByTestId('tree-page')).toHaveAttribute('data-opened', '1');
  await expect(page.getByTestId('tree-row')).toHaveCount(5);
  await expect(page.getByTestId('tree-action-error')).toHaveCount(0);
});

test('the Compact outline', async ({ page, request, baseURL }) => {
  const origin = baseURL ?? '';
  await setup(request, baseURL);
  const csrf = await loginAs(page, baseURL, 'alice');
  const patch = await page.request.patch('/api/me/settings', {
    headers: { Origin: origin, ...sameOrigin, 'X-PCC-CSRF': csrf },
    data: { ui_prefs: { format: 'compact' } },
  });
  expect(patch.status()).toBe(200);
  const tv = await openTree(page, await refOf(page, 'The focal post'));
  await expect(page.getByTestId('tree-layout')).toHaveAttribute('data-format', 'compact');
  await expect(page.getByTestId('tree-row')).toHaveCount(5);
  const focalItem = rowOf(page, 0).locator('[data-centered="true"]');
  for (const id of ['integrity-chain', 'integrity-inviter-label', 'integrity-why', 'integrity-audience', 'integrity-report']) {
    await expect(focalItem.getByTestId(id)).toBeVisible();
  }
  // A generation collapses and opens again.
  const toggle = rowOf(page, 1).getByTestId('tree-row-toggle');
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await expect(rowOf(page, 1).getByTestId('tree-item').first()).toBeHidden();
  await toggle.click();
  // "Show its replies" re-centers the row: the replies to replies reload.
  const second = tv.rows[3]?.items[1]?.post.ref ?? '';
  const row = page.waitForResponse(isRow);
  await rowOf(page, 1).locator(`[data-testid="tree-item"][data-ref="${second}"]`).getByTestId('tree-center').click();
  expect(new URL((await row).url()).searchParams.get('center')).toBe(second);
  await expect(page.getByTestId('tree-row')).toHaveCount(4);
});
