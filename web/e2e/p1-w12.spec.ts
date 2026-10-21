// P1-W12 end to end, through the real binary (PCC_ENV=test: dev scanner, fs
// blob stores, the Go /m/{key} media origin). Fixture: mia – noa (friend),
// mia – pia (friend+), noa – quin (quin is 2 steps from mia).
//  1. mia attaches two pictures in the composer (uploaded through the media
//     picker, one removed again) and publishes; the post carries the picture
//     and its alt text, noa sees it at display size in his feed under refs of
//     his own and gets a signed URL; quin (2 steps) too.
//  2. A friend+ post is shown to pia and hidden from noa and quin.
//  3. noa cannot attach mia's media (her ref or his ref for it): the uniform
//     not-found, and nothing is posted; a fifth picture is too_many_media.
//  4. Once mia deletes the post, noa's next URL request is the uniform 404.
// Every image is generated in code. Selectors avoid English text so the spec
// runs in en and in en-XA.
import { deflateSync } from 'node:zlib';
import { expect, request as pwRequest, test, type APIRequestContext, type Page } from '@playwright/test';
import { World } from './fixtures/world';

const sameOrigin = { 'Sec-Fetch-Site': 'same-origin' };
const notFound = '{"error":"not_found"}\n';
let origin = '';

test.beforeEach(({ baseURL }) => {
  origin = baseURL ?? '';
});

// ---- generated images ----

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

// png builds a 64×40 RGB PNG whose colours depend on seed.
function png(seed: number): Buffer {
  const w = 64;
  const h = 40;
  const raw = Buffer.alloc(h * (1 + 3 * w));
  let o = 0;
  for (let y = 0; y < h; y++) {
    raw[o++] = 0;
    for (let x = 0; x < w; x++) {
      raw[o++] = (x * 4 + seed * 37) & 255;
      raw[o++] = (y * 6) & 255;
      raw[o++] = (seed * 91) & 255;
    }
  }
  const ihdr = Buffer.concat([u32(w), u32(h), Buffer.from([8, 2, 0, 0, 0])]);
  return Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    chunk('IHDR', ihdr),
    chunk('IDAT', deflateSync(raw)),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

const file = (seed: number) => ({ name: `p${String(seed)}.png`, mimeType: 'image/png', buffer: png(seed) });

// ---- helpers ----

interface MediaJSON {
  ref: string;
  w: number;
  h: number;
  alt?: string;
}

interface PostJSON {
  ref: string;
  body: string;
  audience: string;
  media?: MediaJSON[];
}

async function signIn(page: Page, handle: string): Promise<string> {
  await page.context().clearCookies();
  return new World(page.request, origin).login(handle);
}

// as returns a fresh request context signed in as handle, with its CSRF headers.
async function as(handle: string): Promise<{ req: APIRequestContext; h: Record<string, string> }> {
  const req = await pwRequest.newContext({ baseURL: origin });
  const csrf = await new World(req, origin).login(handle);
  return { req, h: { Origin: origin, ...sameOrigin, 'X-PCC-CSRF': csrf } };
}

async function getJSON<T>(req: APIRequestContext, path: string): Promise<T> {
  const res = await req.get(path, { headers: sameOrigin });
  expect(res.status(), path).toBe(200);
  return (await res.json()) as T;
}

// postsOf lists the posts of the friend with handle as viewer sees them.
async function postsOf(req: APIRequestContext, handle: string): Promise<PostJSON[]> {
  const people = await getJSON<{ items: { ref: string; handle: string }[] }>(req, '/api/friends');
  const ref = people.items.find((f) => f.handle === handle)?.ref;
  expect(ref, handle).toBeDefined();
  return (await getJSON<{ items: PostJSON[] }>(req, `/api/people/${ref ?? ''}/posts`)).items;
}

interface FeedJSON {
  items: { type: string; post?: PostJSON; thread?: PostJSON[] }[];
}

// feedPosts is every post on the first feed page of the viewer.
async function feedPosts(req: APIRequestContext): Promise<PostJSON[]> {
  const page = await getJSON<FeedJSON>(req, '/api/feed');
  return page.items.flatMap((it) => [...(it.post ? [it.post] : []), ...(it.thread ?? [])]);
}

async function uploadAPI(req: APIRequestContext, h: Record<string, string>, seed: number): Promise<MediaJSON> {
  const res = await req.post('/api/media', { headers: h, multipart: { file: file(seed), alt: '' } });
  expect(res.status()).toBe(201);
  return (await res.json()) as MediaJSON;
}

async function urlStatus(req: APIRequestContext, ref: string, variant = 'display'): Promise<number> {
  return (await req.get(`/api/media/${ref}/url?variant=${variant}`, { headers: sameOrigin })).status();
}

async function loaded(page: Page, scope: string, variant: string) {
  await expect
    .poll(() =>
      page
        .locator(`${scope} img[data-variant=${variant}]`)
        .first()
        .evaluate((i: HTMLImageElement) => i.complete && i.naturalWidth > 0),
    )
    .toBe(true);
}

test('pictures attach in the composer, follow the post to friends, and its friend+ audience stays with friend+', async ({ page }) => {
  test.setTimeout(120_000);
  const w = new World(await pwRequest.newContext({ baseURL: origin }), origin);
  await w.reset();
  await w.seed('p1-w12');

  // 1. mia composes a post with pictures.
  await signIn(page, 'mia');
  await page.goto(`${origin}/compose`);
  await page.getByTestId('composer-body').fill('Two pictures from the walk');
  await page.getByTestId('media-file').setInputFiles([file(1), file(2)]);
  await page.getByTestId('media-alt').first().fill('a striped pattern');
  const uploads: MediaJSON[] = [];
  page.on('response', async (r) => {
    if (r.url().endsWith('/api/media') && r.request().method() === 'POST' && r.status() === 201) {
      uploads.push((await r.json()) as MediaJSON);
    }
  });
  await page.getByTestId('media-upload').click();
  await expect(page.getByTestId('composer-media-item')).toHaveCount(2);
  await loaded(page, '[data-testid=composer-media]', 'thumb');
  // The second picture is removed again; the first is posted.
  await page.getByTestId('composer-media-remove').nth(1).click();
  await expect(page.getByTestId('composer-media-item')).toHaveCount(1);
  const created = page.waitForResponse((r) => r.url().endsWith('/api/posts') && r.request().method() === 'POST');
  await page.getByTestId('composer-publish').click();
  const res = await created;
  expect(res.status()).toBe(201);
  const post = (await res.json()) as PostJSON;
  expect(post.media).toHaveLength(1);
  expect(post.media?.[0]).toMatchObject({ w: 64, h: 40, alt: 'a striped pattern' });
  expect(uploads).toHaveLength(2);
  expect(post.media?.[0]?.ref).toBe(uploads[0]?.ref);
  await expect(page.getByTestId('composer-published')).toBeVisible();
  await loaded(page, '[data-testid=post-preview-media]', 'thumb');

  // A friend+ post, chosen in the same composer.
  await page.getByTestId('composer-again').click();
  await page.getByTestId('composer-body').fill('Only for friend+');
  await page.getByTestId('post-audience').selectOption('friend_plus');
  const plus = page.waitForResponse((r) => r.url().endsWith('/api/posts') && r.request().method() === 'POST');
  await page.getByTestId('composer-publish').click();
  expect(((await (await plus).json()) as PostJSON).audience).toBe('friend_plus');

  // noa sees the picture at display size in his feed, under his own ref.
  await signIn(page, 'noa');
  await page.goto(`${origin}/`);
  const card = page.locator('[data-testid=feed-post]').filter({ has: page.getByTestId('feed-media') });
  await expect(card).toHaveCount(1);
  const img = card.getByTestId('feed-media').locator('img[data-variant=display]');
  await expect(img).toHaveAttribute('alt', 'a striped pattern');
  await loaded(page, '[data-testid=feed-media]', 'display');
  const noa = await as('noa');
  const noaSees = await postsOf(noa.req, 'mia');
  expect(noaSees.map((p) => p.body)).toEqual(['Two pictures from the walk']);
  const noaRef = noaSees[0]?.media?.[0]?.ref ?? '';
  expect(noaRef).not.toBe('');
  expect(noaRef).not.toBe(post.media?.[0]?.ref);
  expect(await urlStatus(noa.req, noaRef)).toBe(200);
  // mia's own ref means nothing to noa.
  expect(await urlStatus(noa.req, post.media?.[0]?.ref ?? '')).toBe(404);

  // quin, two steps away, sees the picture too (under a ref of his own).
  const quin = await as('quin');
  const quinSees = await feedPosts(quin.req);
  const quinPost = quinSees.find((p) => p.body === 'Two pictures from the walk');
  expect(quinPost?.media).toHaveLength(1);
  expect(await urlStatus(quin.req, quinPost?.media?.[0]?.ref ?? '')).toBe(200);

  // 2. pia (friend+) sees both posts; noa and quin never see the friend+ one.
  const pia = await as('pia');
  expect((await postsOf(pia.req, 'mia')).map((p) => p.audience).sort()).toEqual(['connected', 'friend_plus']);
  expect((await feedPosts(pia.req)).map((p) => p.body)).toContain('Only for friend+');
  for (const viewer of [noa, quin]) {
    expect((await feedPosts(viewer.req)).map((p) => p.body)).not.toContain('Only for friend+');
  }

  // 3. noa cannot attach mia's media, not with her ref nor his; nothing is posted.
  for (const ref of [post.media?.[0]?.ref ?? '', noaRef, uploads[1]?.ref ?? '']) {
    const r = await noa.req.post('/api/posts', { headers: noa.h, data: { body: 'not mine', lang: 'en', media: [ref] } });
    expect(r.status()).toBe(404);
    expect(await r.text()).toBe(notFound);
  }
  // Five of his own pictures are too many; four are fine.
  const mine = [];
  for (let i = 0; i < 5; i++) mine.push((await uploadAPI(noa.req, noa.h, 10 + i)).ref);
  const five = await noa.req.post('/api/posts', { headers: noa.h, data: { body: 'five', lang: 'en', media: mine } });
  expect(five.status()).toBe(422);
  expect(((await five.json()) as { error: string }).error).toBe('too_many_media');
  const four = await noa.req.post('/api/posts', { headers: noa.h, data: { body: 'four', lang: 'en', media: mine.slice(0, 4) } });
  expect(four.status()).toBe(201);
  expect(((await four.json()) as PostJSON).media).toHaveLength(4);
  expect((await postsOf(pia.req, 'mia')).length).toBe(2);

  // 4. mia deletes the post: noa's next URL request is the uniform 404.
  const mia = await as('mia');
  const del = await mia.req.delete(`/api/posts/${post.ref}`, { headers: mia.h });
  expect(del.status()).toBe(204);
  const gone = await noa.req.get(`/api/media/${noaRef}/url?variant=display`, { headers: sameOrigin });
  expect(gone.status()).toBe(404);
  expect(await gone.text()).toBe(notFound);
});
