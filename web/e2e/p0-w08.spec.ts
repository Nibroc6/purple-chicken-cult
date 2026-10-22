// P0-W08 end to end: the reply tree (/p/:ref) — rows around the focal post
// (parents above with the primary parent's siblings in the focal row, replies
// below, ranked by distance tier then the reader's preset) drawn as one
// connected canvas with the focal post highlighted and shown in full below,
// "+N more" loading the rest of a generation, earlier / later generations, a
// diamond shown once (a dashed edge to its node), the focal post's other
// parents joined by an "also" edge, no trace of hidden parents or children,
// clicking or Enter on a post bringing it into focus (a drag does not), a long
// post as an excerpt on the map and in full once in focus, viewer-bound
// snapshots with a reopen on expiry, and the same canvas in Compact. Selectors
// avoid English text so the spec runs in en and en-XA (post bodies are user
// content).
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

const nodeOf = (page: Page, ref: string): Locator => page.locator(`[data-testid="explorer-node"][data-ref="${ref}"]`);
const nodeWith = (page: Page, text: string): Locator => page.getByTestId('explorer-node').filter({ hasText: text });
const focusCard = (page: Page): Locator => page.getByTestId('tree-focus');
const bodies = (r: RowJSON | undefined) => (r?.items ?? []).map((it) => (it.shown_above ? `^${it.post.author.handle}` : it.post.body));
const longBody = Array.from({ length: 12 }, (_, i) => `Long reply line ${String(i + 1)}`).join('\n\n');

test('rows around the focal post, ranked by tier, on one connected canvas', async ({ page, request, baseURL }) => {
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

  // The page: one canvas, every loaded post a node once, the focal post highlighted and in full below.
  await expect(page.getByTestId('tree-row')).toHaveCount(0);
  await expect(page.getByTestId('explorer-node')).toHaveCount(16);
  await expect(nodeOf(page, focal)).toHaveAttribute('data-focal', 'true');
  await expect(focusCard(page)).toHaveAttribute('data-ref', focal);
  await expect(focusCard(page).locator('.post-focal')).toBeVisible();
  await expect(page.getByText('Xavier is never shown')).toHaveCount(0);
  for (const id of ['integrity-chain', 'integrity-why', 'integrity-audience', 'integrity-report']) {
    await expect(focusCard(page).getByTestId(id)).toBeVisible();
  }

  // "+N more" counts the visible replies beyond the first page and loads them.
  const chip = page.getByTestId('explorer-more');
  await expect(chip).toHaveAttribute('data-count', '2');
  // It sits at the end of the row, off screen: fold the focus card and show the whole tree first.
  await page.getByTestId('tree-focus-toggle').click();
  await page.getByTestId('explorer-fit').click();
  const more = page.waitForResponse(isMore);
  await chip.click();
  const page2 = (await (await more).json()) as RowJSON;
  expect(bodies(page2)).toEqual(['Carol reply 11', 'Carol reply 10']);
  expect(page2.more).toBe(0);
  await expect(page.getByTestId('explorer-node')).toHaveCount(18);
  await expect(page.getByTestId('explorer-more')).toHaveCount(0);

  // Earlier generations load at the top edge, until there are none.
  const earlier = page.waitForResponse(isRow);
  await page.getByTestId('tree-earlier').click();
  const up = (await (await earlier).json()) as RowJSON;
  expect([up.gen, up.label, bodies(up)]).toEqual([-3, 'generations_up', ['Root by carol']]);
  await expect(nodeWith(page, 'Root by carol')).toHaveCount(1);
  await expect(page.getByTestId('tree-earlier')).toHaveCount(0);
});

test('a diamond shows once, other parents join the focal post, hidden parents look like roots', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  const focal = await refOf(page, 'The focal post');
  await openTree(page, focal);

  // The diamond is a reply and a reply to a reply: one node, a dashed edge for its second spot.
  await expect(nodeWith(page, 'Diamond by carol')).toHaveCount(1);
  await expect(page.locator('.explorer-edge[data-kind="above"]')).toHaveCount(1);
  // carol's second parent joins the focal post apart from the siblings' line.
  await expect(nodeWith(page, 'Second parent by carol')).toHaveCount(1);
  await expect(page.locator('.explorer-edge[data-kind="also"]')).toHaveCount(1);

  // carol's reply to dave's friends-only post: no parents, nothing earlier.
  const under = await refOf(page, 'Carol under a hidden parent');
  const root = await openTree(page, under);
  expect(root.rows.map((r) => r.gen)).toEqual([0]);
  expect(root.has_earlier || root.has_later).toBe(false);
  await expect(page.getByTestId('explorer-node')).toHaveCount(1);
  await expect(page.locator('.explorer-edge')).toHaveCount(0);
  await expect(page.getByTestId('tree-earlier')).toHaveCount(0);
  await expect(page.getByText("Dave's secret")).toHaveCount(0);
});

test('clicking or pressing Enter on a post brings it into focus; a drag does not', async ({ page, request, baseURL }) => {
  await page.setViewportSize({ width: 420, height: 900 });
  await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  const focal = await refOf(page, 'The focal post');
  const tv = await openTree(page, focal);
  const second = tv.rows[3]?.items[1]?.post.ref ?? '';

  // A drag pans the map and opens nothing.
  const svg = page.getByTestId('explorer-svg');
  const before = await svg.getAttribute('viewBox');
  const box = await svg.boundingBox();
  if (!box) throw new Error('no canvas');
  let opened = 0;
  page.on('response', (r) => {
    if (isOpen(r)) opened++;
  });
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2 + 120, box.y + box.height / 2 + 60, { steps: 5 });
  await page.mouse.up();
  await expect(svg).not.toHaveAttribute('viewBox', before ?? '');
  expect(opened).toBe(0);

  // A click on a reply: the tree reopens around it.
  await page.getByTestId('explorer-recenter').click();
  const reopened = page.waitForResponse(isOpen);
  await nodeOf(page, second).dispatchEvent('click');
  const next = (await (await reopened).json()) as TreeJSON;
  expect(next.focal).toBe(second);
  await expect(page).toHaveURL(new RegExp(`/p/${second}$`));
  await expect(focusCard(page)).toHaveAttribute('data-ref', second);
  await expect(nodeOf(page, second)).toHaveAttribute('data-focal', 'true');
  // The old focal post is now its parent, and Enter on it brings it back.
  await expect(nodeOf(page, focal)).toHaveAttribute('data-gen', '-1');
  const back = page.waitForResponse(isOpen);
  await nodeOf(page, focal).focus();
  await page.keyboard.press('Enter');
  expect(((await (await back).json()) as TreeJSON).focal).toBe(focal);
  await expect(focusCard(page)).toHaveAttribute('data-ref', focal);
});

test('a long post is an excerpt on the map and whole once in focus', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  await openTree(page, await refOf(page, 'The focal post'));
  const long = nodeWith(page, 'Long reply line 1');
  await expect(long).toHaveCount(1);
  await expect(long).not.toContainText('Long reply line 12');
  const reopened = page.waitForResponse(isOpen);
  await long.dispatchEvent('click');
  expect((await reopened).status()).toBe(200);
  await expect(focusCard(page).getByText(/Long reply line 12/)).toBeVisible();
  // The post in focus never folds.
  await expect(focusCard(page).getByTestId('feed-fold-toggle')).toHaveCount(0);
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
  await page.getByTestId('tree-later').click();
  expect((await reopened).status()).toBe(200);
  await expect(page.getByTestId('tree-page')).toHaveAttribute('data-opened', '1');
  await expect(page.getByTestId('explorer-node')).toHaveCount(16);
  await expect(page.getByTestId('tree-action-error')).toHaveCount(0);
});

test('the same canvas in Compact', async ({ page, request, baseURL }) => {
  const origin = baseURL ?? '';
  await setup(request, baseURL);
  const csrf = await loginAs(page, baseURL, 'alice');
  const patch = await page.request.patch('/api/me/settings', {
    headers: { Origin: origin, ...sameOrigin, 'X-PCC-CSRF': csrf },
    data: { ui_prefs: { format: 'compact' } },
  });
  expect(patch.status()).toBe(200);
  const focal = await refOf(page, 'The focal post');
  await openTree(page, focal);
  await expect(page.getByTestId('tree-layout')).toHaveAttribute('data-format', 'compact');
  await expect(page.getByTestId('explorer-node')).toHaveCount(16);
  await expect(nodeOf(page, focal)).toHaveAttribute('data-focal', 'true');
  for (const id of ['integrity-chain', 'integrity-why', 'integrity-audience', 'integrity-report']) {
    await expect(focusCard(page).getByTestId(id)).toBeVisible();
  }
});
