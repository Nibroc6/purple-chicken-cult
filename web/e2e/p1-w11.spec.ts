// P1-W11 end to end, through the real binary (PCC_ENV=test: dev scanner, fs
// blob and evidence stores):
//  1. GFM tables in the feed: header cells with scope and alignment, inline
//     content and escaped pipes in cells, tracking parameters stripped from
//     links in cells; an 11-column table and a table inside a blockquote stay
//     text; a wide table scrolls inside its own box, never the page.
//  2. Preview images: the server fetches the page and its og:image (as
//     pcc-preview/1, once each); the reader's browser never contacts the site.
//  3. A preview image on the dev blocklist (the synthetic pattern of
//     testdata/scan/blocklist.txt; never real imagery) is held and queued, not
//     dropped: the preview shows without an image and the linking author's
//     uploads are refused (media_rejected).
// Every image is generated in code. Selectors avoid English text so the spec
// runs in en and in en-XA.
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import { deflateSync } from 'node:zlib';
import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { World } from './fixtures/world';

const sameOrigin = { 'Sec-Fetch-Site': 'same-origin' };

async function setup(request: APIRequestContext, baseURL: string | undefined): Promise<World> {
  const world = new World(request, baseURL ?? '');
  await world.reset();
  await world.seed('p1-w11');
  return world;
}

async function loginAs(page: Page, baseURL: string | undefined, handle: string): Promise<string> {
  await page.context().clearCookies();
  return new World(page.request, baseURL ?? '').login(handle);
}

function watchConsole(page: Page): string[] {
  const errors: string[] = [];
  page.on('console', (m) => {
    if (m.type() === 'error') errors.push(m.text());
  });
  page.on('pageerror', (e) => errors.push(e.message));
  return errors;
}

async function openFeed(page: Page): Promise<void> {
  const first = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/feed');
  await page.goto('/');
  expect((await first).status()).toBe(200);
}

const card = (page: Page, text: string) => page.locator('[data-testid="feed-post"]', { hasText: text });

// ---- generated images (the same synthetic pattern as p1-w04) ----

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

// rgbPNG builds an RGB PNG of pixel(x, y), filter 0. stored = deflate with
// uncompressed blocks (byte-for-byte deterministic, so its digest is fixed).
function rgbPNG(w: number, h: number, pixel: (x: number, y: number) => [number, number, number], stored: boolean): Buffer {
  const raw = Buffer.alloc(h * (1 + 3 * w));
  let o = 0;
  for (let y = 0; y < h; y++) {
    raw[o++] = 0;
    for (let x = 0; x < w; x++) {
      for (const c of pixel(x, y)) raw[o++] = c;
    }
  }
  let z: Buffer;
  if (stored) {
    const parts: Buffer[] = [Buffer.from([0x78, 0x01])];
    for (let i = 0; i < raw.length || i === 0; i += 65535) {
      const blk = raw.subarray(i, i + 65535);
      const final = i + 65535 >= raw.length ? 1 : 0;
      const hdr = Buffer.alloc(5);
      hdr[0] = final;
      hdr.writeUInt16LE(blk.length, 1);
      hdr.writeUInt16LE(blk.length ^ 0xffff, 3);
      parts.push(hdr, blk);
      if (final) break;
    }
    let a = 1;
    let b = 0;
    for (const c of raw) {
      a = (a + c) % 65521;
      b = (b + a) % 65521;
    }
    parts.push(u32(((b << 16) | a) >>> 0));
    z = Buffer.concat(parts);
  } else {
    z = deflateSync(raw);
  }
  const ihdr = Buffer.concat([u32(w), u32(h), Buffer.from([8, 2, 0, 0, 0])]);
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk('IHDR', ihdr), chunk('IDAT', z), chunk('IEND', Buffer.alloc(0))]);
}

// blockedPNG is the synthetic pattern whose SHA-256 is in testdata/scan/blocklist.txt.
const blockedPNG = () => rgbPNG(32, 32, (x, y) => [x * 8, y * 8, 0xc0], true);
const cleanPNG = (seed: number) => rgbPNG(48, 32, (x, y) => [(x * 5 + seed) & 255, (y * 7) & 255, seed & 255], false);

// ---- a site on 127.0.0.1 (reachable by the server's fetcher only under test hooks) ----

interface Hit {
  path: string;
  ua: string;
}

async function site(image: Buffer): Promise<{ origin: string; hits: Hit[]; close(): Promise<void> }> {
  const hits: Hit[] = [];
  let origin = '';
  const srv = http.createServer((req, res) => {
    hits.push({ path: req.url ?? '', ua: req.headers['user-agent'] ?? '' });
    if ((req.url ?? '').startsWith('/img/')) {
      res.setHeader('Content-Type', 'image/png');
      res.end(image);
      return;
    }
    res.setHeader('Content-Type', 'text/html; charset=utf-8');
    res.end(
      '<!doctype html><html><head><title>Fallback</title>' +
        '<meta property="og:title" content="Pictured article">' +
        `<meta property="og:image" content="${origin}/img/cover.png?utm_source=og">` +
        '</head><body>hi</body></html>',
    );
  });
  await new Promise<void>((r) => srv.listen(0, '127.0.0.1', r));
  const { port } = srv.address() as AddressInfo;
  origin = `http://127.0.0.1:${String(port)}`;
  return {
    origin,
    hits,
    close: () =>
      new Promise<void>((r) => {
        srv.close(() => {
          r();
        });
      }),
  };
}

test('GFM tables render in the feed, within the subset', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  const errors = watchConsole(page);
  await loginAs(page, baseURL, 'alice');
  await openFeed(page);

  const scores = card(page, 'Scores this week');
  const table = scores.getByTestId('md-table');
  await expect(table).toHaveCount(1);
  await expect(table).toHaveAttribute('tabindex', '0');
  await expect(table).toHaveAttribute('role', 'region');
  const ths = table.locator('thead th');
  await expect(ths).toHaveText(['Player', 'Team', 'Points']);
  for (let i = 0; i < 3; i++) await expect(ths.nth(i)).toHaveAttribute('scope', 'col');
  await expect(ths.nth(0)).toHaveAttribute('data-align', 'left');
  await expect(ths.nth(1)).toHaveAttribute('data-align', 'center');
  await expect(ths.nth(2)).toHaveAttribute('data-align', 'right');
  const rows = table.locator('tbody tr');
  await expect(rows).toHaveCount(2);
  const first = rows.nth(0).locator('td');
  await expect(first.nth(0)).toHaveText('Ana');
  await expect(first.nth(2)).toHaveText('31');
  await expect(rows.nth(0).locator('td strong')).toHaveText('Ana');
  await expect(rows.nth(1).locator('td')).toHaveText(['Bo | Bea', 'Blues', '7']);
  // A link in a cell is like any other: real host shown, tracking stripped.
  const link = first.nth(1).locator('a');
  await expect(link).toHaveText('Reds');
  await expect(first.nth(1).getByTestId('md-host')).toContainText('reds.example');
  await expect(link).toHaveAttribute('href', 'https://reds.example/');
  await expect(link).toHaveAttribute('rel', 'noopener noreferrer nofollow ugc');
  // The intro line stays a paragraph above the table.
  await expect(scores.locator('.md > p').first()).toHaveText('Scores this week');

  // Eleven columns and a table inside a blockquote stay text.
  for (const text of ['Too wide', 'Quoted table']) {
    const post = card(page, text);
    await expect(post).toBeVisible();
    await expect(post.locator('table')).toHaveCount(0);
    await expect(post.locator('.md')).toContainText('|');
  }

  // A wide table scrolls inside its box, never the page (phone width).
  await page.setViewportSize({ width: 360, height: 800 });
  const long = card(page, 'Long cells').getByTestId('md-table');
  await expect(long.locator('thead th')).toHaveCount(10);
  await long.scrollIntoViewIfNeeded();
  const box = await long.evaluate((el) => ({ scroll: el.scrollWidth, client: el.clientWidth }));
  expect(box.scroll).toBeGreaterThan(box.client);
  const doc = await page.evaluate(() => ({
    scroll: document.documentElement.scrollWidth,
    client: document.documentElement.clientWidth,
  }));
  expect(doc.scroll).toBeLessThanOrEqual(doc.client);
  // Keyboard users can scroll it.
  await long.focus();
  await page.keyboard.press('ArrowRight');
  await expect.poll(() => long.evaluate((el) => el.scrollLeft)).toBeGreaterThan(0);

  expect(errors).toEqual([]);
});

test('preview images are fetched by the server only', async ({ page, request, baseURL, playwright }) => {
  await setup(request, baseURL);
  const hooks = await playwright.request.newContext({ baseURL });
  const clock = new World(hooks, baseURL ?? '');
  const origin = baseURL ?? '';
  const web = await site(cleanPNG(5));
  try {
    const errors = watchConsole(page);
    const csrf = await new World(request, origin).login('bob');
    const res = await request.post('/api/posts', {
      headers: { Origin: origin, ...sameOrigin, 'X-PCC-CSRF': csrf },
      data: { body: `Pictured ${web.origin}/article`, lang: 'en' },
    });
    expect(res.status()).toBe(201);

    // The job fetches the page, then its image (canonical: no utm_source).
    await expect.poll(() => web.hits.length, { timeout: 15_000 }).toBe(2);
    expect(web.hits).toEqual([
      { path: '/article', ua: 'pcc-preview/1' },
      { path: '/img/cover.png', ua: 'pcc-preview/1' },
    ]);

    await clock.advance('30s');
    await loginAs(page, origin, 'alice');
    await openFeed(page);
    const preview = card(page, 'Pictured').getByTestId('md-preview');
    await expect(preview).toHaveCount(1);
    await expect(preview).toContainText('Pictured article');
    await page.waitForLoadState('networkidle');

    // The reader's browser never contacted the site.
    expect(web.hits.map((h) => h.ua)).toEqual(['pcc-preview/1', 'pcc-preview/1']);
    expect(errors).toEqual([]);
  } finally {
    await web.close();
    await hooks.dispose();
  }
});

test('a blocklisted preview image is held and queued, and the preview shows no image', async ({ page, request, baseURL, playwright }) => {
  await setup(request, baseURL);
  const hooks = await playwright.request.newContext({ baseURL });
  const clock = new World(hooks, baseURL ?? '');
  const origin = baseURL ?? '';
  const web = await site(blockedPNG());
  try {
    const csrf = await new World(request, origin).login('carol');
    const res = await request.post('/api/posts', {
      headers: { Origin: origin, ...sameOrigin, 'X-PCC-CSRF': csrf },
      data: { body: `Look ${web.origin}/article`, lang: 'en' },
    });
    expect(res.status()).toBe(201);
    await expect.poll(() => web.hits.map((h) => h.path), { timeout: 15_000 }).toEqual(['/article', '/img/cover.png']);

    // The linking author is held: a clean upload is refused.
    const upload = async () =>
      request.post('/api/media', {
        headers: { ...sameOrigin, Origin: origin, 'X-PCC-CSRF': csrf },
        multipart: { file: { name: 'clean.png', mimeType: 'image/png', buffer: cleanPNG(9) }, alt: '' },
      });
    await expect
      .poll(async () => {
        const r = await upload();
        return r.status() === 422 ? ((await r.json()) as { error: string }).error : String(r.status());
      })
      .toBe('media_rejected');

    // The preview still appears at its bucket, with no image.
    await clock.advance('30s');
    await loginAs(page, origin, 'alice');
    await openFeed(page);
    const post = card(page, 'Look');
    await expect(post.getByTestId('md-preview')).toHaveCount(1);
    await expect(post.getByTestId('md-preview')).toContainText('Pictured article');
    await expect(post.locator('img')).toHaveCount(0);
  } finally {
    await web.close();
    await hooks.dispose();
  }
});
