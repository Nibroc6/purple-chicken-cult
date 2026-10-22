// P0-W06 end to end: the in-network like summary (2-hop counting, friends named
// only when they allow it), like and unlike, the seen tracker on a card (only
// the card's own post, never its context row) feeding the recently-seen page,
// hide and show less, and the server's uniform answers (POST /seen with garbage
// is 204). Selectors avoid English text so the spec runs in en and en-XA.
import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { World } from './fixtures/world';

interface PostJSON {
  ref: string;
  body: string;
  context?: { ref: string; author_handle: string }[];
  likes: { in_network: number; names?: { handle: string }[]; others: number; by_me: boolean };
}

interface PersonJSON {
  ref: string;
  handle: string;
}

const sameOrigin = { 'Sec-Fetch-Site': 'same-origin' };

function headers(origin: string, csrf: string): Record<string, string> {
  return { Origin: origin, ...sameOrigin, 'X-PCC-CSRF': csrf };
}

// setup resets the run and loads the fixture: alice's friends bob, erin (does
// not want to be named in likes) and fran; bob – carol – dave; xavier apart.
// bob's "liked" post has likes from carol (2 hops from alice), dave (3 hops),
// erin and fran.
async function setup(request: APIRequestContext, baseURL: string | undefined): Promise<void> {
  const world = new World(request, baseURL ?? '');
  await world.reset();
  await world.seed('p0-w06');
}

async function loginAs(page: Page, baseURL: string | undefined, handle: string): Promise<string> {
  await page.context().clearCookies();
  return new World(page.request, baseURL ?? '').login(handle);
}

async function getJSON<T>(req: APIRequestContext, path: string): Promise<T> {
  const res = await req.get(path, { headers: sameOrigin });
  expect(res.status(), path).toBe(200);
  return (await res.json()) as T;
}

// bobPost finds one of bob's posts, as the viewer sees it, by its body.
async function bobPost(req: APIRequestContext, body: string): Promise<PostJSON> {
  const friends = await getJSON<{ items: PersonJSON[] }>(req, '/api/friends');
  const bob = friends.items.find((f) => f.handle === 'bob');
  expect(bob).toBeDefined();
  const posts = (await getJSON<{ items: PostJSON[] }>(req, `/api/people/${bob?.ref ?? ''}/posts`)).items;
  const p = posts.find((x) => x.body === body);
  expect(p, body).toBeDefined();
  return p as PostJSON;
}

async function recentlySeen(req: APIRequestContext): Promise<string[]> {
  return (await getJSON<{ items: PostJSON[] }>(req, '/api/seen/recent')).items.map((p) => p.ref);
}

test('likes count only within two hops and name only friends who allow it; like and unlike', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  const liked = await bobPost(page.request, 'A post your network liked');
  // carol (2 hops), erin and fran count; dave (3 hops) does not; only fran is named.
  expect(liked.likes).toMatchObject({ in_network: 3, others: 2, by_me: false });
  expect(liked.likes.names?.map((n) => n.handle)).toEqual(['fran']);

  await page.goto(`/p/${liked.ref}/likes`);
  const summary = page.getByTestId('engage-likes');
  await expect(summary).toHaveAttribute('data-count', '3');
  await expect(page.getByTestId('engage-liker')).toHaveCount(1);
  await expect(page.getByTestId('engage-liker')).toHaveAttribute('href', /^\/people\//);

  const like = page.getByTestId('engage-like');
  await expect(like).toHaveAttribute('aria-pressed', 'false');
  const put = page.waitForResponse((r) => r.request().method() === 'PUT' && r.url().endsWith('/like'));
  await like.click();
  expect((await put).status()).toBe(200);
  await expect(like).toHaveAttribute('aria-pressed', 'true');
  await expect(summary).toHaveAttribute('data-count', '4');

  // It sticks across a reload, then unliking takes it back.
  await page.reload();
  await expect(page.getByTestId('engage-like')).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByTestId('engage-likes')).toHaveAttribute('data-count', '4');
  await page.getByTestId('engage-like').click();
  await expect(page.getByTestId('engage-like')).toHaveAttribute('aria-pressed', 'false');
  await expect(page.getByTestId('engage-likes')).toHaveAttribute('data-count', '3');
  expect((await bobPost(page.request, 'A post your network liked')).likes.by_me).toBe(false);
});

test('the like summary opens the likes modal: deep link, Escape and back close it, a sheet on phones', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  const liked = await bobPost(page.request, 'A post your network liked');
  await page.goto(`/p/${liked.ref}/likes`);
  const open = page.getByTestId('engage-likes-open');
  await open.click();
  const modal = page.getByTestId('likes-modal');
  await expect(modal).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`\\?likes=${liked.ref}$`));
  await expect(modal.getByTestId('likes-modal-count')).toHaveAttribute('data-count', '3');
  await expect(modal.getByTestId('likes-modal-liker')).toHaveCount(1);
  // Escape closes it and focus goes back to the summary.
  await page.keyboard.press('Escape');
  await expect(modal).toHaveCount(0);
  await expect(open).toBeFocused();
  // The back button closes it too.
  await open.click();
  await expect(modal).toBeVisible();
  await page.goBack();
  await expect(modal).toHaveCount(0);
  await expect(page).toHaveURL(new RegExp(`/p/${liked.ref}/likes$`));
  // A deep link opens it over the page; on a phone it fills the screen.
  await page.setViewportSize({ width: 390, height: 800 });
  await page.goto(`/?likes=${liked.ref}`);
  await expect(modal).toBeVisible();
  const box = await modal.boundingBox();
  expect(box?.width).toBe(390);
  await modal.getByTestId('modal-close').click();
  await expect(modal).toHaveCount(0);
  await expect(page).toHaveURL(/\/$/);
});

test('a card seen for a second lands in recently seen; its context row does not', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  const reply = await bobPost(page.request, 'Replying to carol');
  expect(reply.context?.[0]?.author_handle).toBe('carol');

  await page.goto('/seen');
  await expect(page.getByTestId('seen-empty')).toBeVisible();

  await page.goto(`/p/${reply.ref}/likes`);
  const card = page.getByTestId('engage-card');
  await expect(card).toHaveAttribute('data-seen-ref', reply.ref);
  await expect(card.getByTestId('feed-context').first()).toBeVisible();
  await expect(page.locator('[data-seen-ref]')).toHaveCount(1);
  // ≥ 1 s visible, then the 5 s batch (or the flush on hide) sends it.
  await expect
    .poll(() => recentlySeen(page.request), { timeout: 15_000, intervals: [500] })
    .toEqual([reply.ref]);

  await page.goto('/seen');
  const items = page.getByTestId('seen-item');
  await expect(items).toHaveCount(1);
  await expect(items.first()).toHaveAttribute('data-ref', reply.ref);
  await expect(page.locator('[data-seen-ref]')).toHaveCount(0);

  // Leaving a page flushes what is pending right away (keepalive on hide).
  const other = await bobPost(page.request, 'Another post by bob');
  await page.goto(`/p/${other.ref}/likes`);
  await expect(page.locator(`[data-seen-ref="${other.ref}"]`)).toBeVisible();
  await page.waitForTimeout(1_500);
  await page.goto('about:blank');
  await expect
    .poll(() => recentlySeen(page.request), { timeout: 10_000, intervals: [250] })
    .toEqual([other.ref, reply.ref]);
});

test('hide and show less take the post away for the reader only', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  const other = await bobPost(page.request, 'Another post by bob');
  const liked = await bobPost(page.request, 'A post your network liked');

  await page.goto(`/p/${other.ref}/likes`);
  const hide = page.waitForResponse((r) => r.url().endsWith('/hide'));
  await page.getByTestId('engage-hide').click();
  expect((await hide).status()).toBe(204);
  await expect(page.getByTestId('engage-hidden')).toHaveAttribute('data-kind', 'hide');
  await expect(page.getByTestId('engage-like')).toHaveCount(0);

  await page.goto(`/p/${liked.ref}/likes`);
  const less = page.waitForResponse((r) => r.url().endsWith('/show-less'));
  await page.getByTestId('engage-show-less').click();
  expect((await less).status()).toBe(204);
  await expect(page.getByTestId('engage-hidden')).toHaveAttribute('data-kind', 'show_less');
});

test('the server answers garbage and invisible refs uniformly', async ({ page, request, baseURL }) => {
  const origin = baseURL ?? '';
  await setup(request, baseURL);
  const csrf = await loginAs(page, baseURL, 'alice');
  const liked = await bobPost(page.request, 'A post your network liked');
  const h = headers(origin, csrf);

  // POST /seen: 204 whatever the refs, and garbage records nothing.
  const garbage = await page.request.post('/api/seen', { headers: h, data: { posts: ['AAAA', '', `${liked.ref}x`] } });
  expect(garbage.status()).toBe(204);
  expect(await recentlySeen(page.request)).toEqual([]);
  const many = await page.request.post('/api/seen', { headers: h, data: { posts: Array.from({ length: 60 }, () => liked.ref) } });
  expect(many.status()).toBe(204);
  expect(await recentlySeen(page.request)).toEqual([liked.ref]);

  // Likes, hides and show-less on a ref that does not open: the uniform 404.
  const answers = new Set<string>();
  for (const [method, suffix] of [
    ['PUT', '/like'],
    ['DELETE', '/like'],
    ['GET', '/likes'],
    ['POST', '/hide'],
    ['POST', '/show-less'],
  ] as const) {
    for (const ref of ['AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA', `${liked.ref}x`]) {
      const res = await page.request.fetch(`/api/posts/${ref}${suffix}`, { method, headers: h });
      answers.add(`${String(res.status())} ${await res.text()}`);
    }
  }
  expect([...answers]).toEqual(['404 {"error":"not_found"}\n']);
  await page.goto('/p/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA/likes');
  await expect(page.getByTestId('engage-like')).toHaveCount(0);
});
