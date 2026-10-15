// P0-W09 end to end: the Markdown subset as readers see it in the feed (a
// heading-only post at paragraph size, raw HTML shown literally, javascript:
// and data: links as plain text, tracking parameters stripped from hrefs, the
// real host next to link text with punycode decoded and the mixed-script
// flag, no bare-domain linking, images as plain links that are never loaded,
// fenced code folded past 10 lines by keyboard) and server-built link
// previews (fetched once by the server only, never for <url>, shown exactly at
// the fixed delay bucket and not before, with a cleaned title and stripped
// URL). No Trusted Types violation or other console error on the way.
// Selectors avoid English UI text so the spec runs in en and en-XA.
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { World } from './fixtures/world';

const sameOrigin = { 'Sec-Fetch-Site': 'same-origin' };

async function setup(request: APIRequestContext, baseURL: string | undefined): Promise<World> {
  const world = new World(request, baseURL ?? '');
  await world.reset();
  await world.seed('p0-w09');
  return world;
}

async function loginAs(page: Page, baseURL: string | undefined, handle: string): Promise<string> {
  await page.context().clearCookies();
  return new World(page.request, baseURL ?? '').login(handle);
}

// watchConsole fails the test on any console error (a Trusted Types
// violation from an HTML sink shows up here).
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

test('the Markdown subset renders safely', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  const errors = watchConsole(page);
  await loginAs(page, baseURL, 'alice');
  await openFeed(page);

  // A heading-only post is a paragraph-sized line, not a heading element.
  const heading = card(page, 'Only a heading');
  await expect(heading.locator('h1, h2, h3, h4, h5, h6')).toHaveCount(0);
  const hp = heading.locator('p.md-heading');
  await expect(hp).toHaveText('Only a heading');
  const plain = card(page, 'Just a normal paragraph').locator('.md p').first();
  const size = (l: typeof hp) => l.evaluate((el) => getComputedStyle(el).fontSize);
  expect(await size(hp)).toBe(await size(plain));
  expect(await hp.evaluate((el) => getComputedStyle(el).fontWeight)).toBe('600');

  // Raw HTML stays literal text.
  const html = card(page, 'stay text');
  await expect(html.locator('.md')).toContainText('Raw <b>bold</b> and <img src=x onerror=alert(1)> stay text');
  await expect(html.locator('.md b, .md img')).toHaveCount(0);

  // javascript: and data: links are text without an href.
  const schemes = card(page, 'Bad links');
  await expect(schemes.locator('.md a')).toHaveCount(0);
  await expect(schemes.getByTestId('md-nolink')).toHaveCount(2);
  await expect(schemes.locator('.md')).toContainText('js link');

  // hrefs: tracking parameters stripped, no referrer, real hosts shown,
  // punycode decoded and flagged; www. stays text.
  const links = card(page, 'Tracked');
  const anchors = links.locator('.md a');
  await expect(anchors).toHaveCount(2);
  await expect(anchors.nth(0)).toHaveAttribute('href', 'https://news.example/a?id=7');
  await expect(anchors.nth(1)).toHaveAttribute('href', 'https://xn--pple-43d.com/login');
  for (const i of [0, 1]) {
    await expect(anchors.nth(i)).toHaveAttribute('rel', 'noopener noreferrer nofollow ugc');
    await expect(anchors.nth(i)).toHaveAttribute('referrerpolicy', 'no-referrer');
    await expect(anchors.nth(i)).not.toHaveAttribute('title', /.*/);
  }
  const hosts = links.getByTestId('md-host');
  await expect(hosts).toHaveCount(2);
  await expect(hosts.nth(0)).toContainText('news.example');
  await expect(hosts.nth(0)).toHaveAttribute('data-lookalike', 'false');
  await expect(hosts.nth(1)).toContainText('аpple.com');
  await expect(hosts.nth(1)).toHaveAttribute('data-lookalike', 'true');
  await expect(links.locator('.md')).toContainText('www.example.com');

  // Images are links, never loaded.
  const image = card(page, 'Picture');
  await expect(image.locator('img')).toHaveCount(0);
  await expect(image.locator('.md a[data-kind="image"]')).toHaveAttribute('href', 'https://img.example/cat.png');

  // Fenced code past 10 lines is folded; the toggle works by keyboard.
  const code = card(page, 'Code below');
  const fold = code.getByTestId('feed-fold-toggle');
  if ((await fold.count()) > 0) {
    await fold.focus();
    await page.keyboard.press('Enter');
  }
  const block = code.getByTestId('md-code');
  await expect(block).toHaveAttribute('data-collapsed', 'true');
  await expect(block.locator('code')).not.toContainText('l11');
  const toggle = code.getByTestId('md-code-toggle');
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await toggle.focus();
  await page.keyboard.press('Enter');
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  await expect(block.locator('code')).toContainText('l12');

  expect(errors).toEqual([]);
});

interface Hit {
  path: string;
  ua: string;
}

// site serves an article on 127.0.0.1 (reachable by the server's fetcher only
// under test hooks) and records every request.
async function site(): Promise<{ origin: string; hits: Hit[]; close(): Promise<void> }> {
  const hits: Hit[] = [];
  const srv = http.createServer((req, res) => {
    hits.push({ path: req.url ?? '', ua: req.headers['user-agent'] ?? '' });
    res.setHeader('Content-Type', 'text/html; charset=utf-8');
    res.end(
      '<!doctype html><html><head><title>Fallback</title>' +
        '<meta property="og:title" content="  The \u202EE2E\u200B   article  ">' +
        '<meta property="og:description" content="What the server saw"></head><body>hi</body></html>',
    );
  });
  await new Promise<void>((r) => srv.listen(0, '127.0.0.1', r));
  const { port } = srv.address() as AddressInfo;
  return {
    origin: `http://127.0.0.1:${String(port)}`,
    hits,
    close: () => new Promise<void>((r) => srv.close(() => { r(); })),
  };
}

test('link previews are built by the server and appear only at the fixed delay', async ({ page, request, baseURL, playwright }) => {
  await setup(request, baseURL);
  // The clock hook goes through a context without a session (an unsafe call
  // with a session cookie would need the CSRF header).
  const hooks = await playwright.request.newContext({ baseURL });
  const clock = new World(hooks, baseURL ?? '');
  const origin = baseURL ?? '';
  const web = await site();
  try {
    const errors = watchConsole(page);
    const csrf = await new World(request, baseURL ?? '').login('bob');
    const res = await request.post('/api/posts', {
      headers: { Origin: origin, ...sameOrigin, 'X-PCC-CSRF': csrf },
      data: { body: `Read ${web.origin}/article?utm_source=e2e&id=1 but not <${web.origin}/quiet>`, lang: 'en' },
    });
    expect(res.status()).toBe(201);

    // The job runs in the background; only the server fetches, once.
    await expect.poll(() => web.hits.length, { timeout: 15_000 }).toBe(1);
    expect(web.hits[0]).toEqual({ path: '/article?id=1', ua: 'pcc-preview/1' });

    // Fetched, but the bucket (30 s on the server clock) has not passed.
    await loginAs(page, baseURL, 'alice');
    await openFeed(page);
    const post = card(page, 'but not');
    await expect(post).toBeVisible();
    await expect(post.getByTestId('feed-preview')).toHaveCount(0);

    await clock.advance('29s');
    await openFeed(page);
    await expect(card(page, 'but not').getByTestId('feed-preview')).toHaveCount(0);

    await clock.advance('1s');
    await openFeed(page);
    const preview = card(page, 'but not').getByTestId('feed-preview');
    await expect(preview).toHaveCount(1);
    await expect(preview).toContainText('The E2E article');
    await expect(preview).toContainText('What the server saw');
    await expect(preview).toContainText('127.0.0.1');
    await expect(preview.locator('a')).toHaveAttribute('href', `${web.origin}/article?id=1`);
    const title = await preview.locator('.preview-title').textContent();
    expect(title).toBe('The E2E article'); // bidi and zero-width characters removed

    // The <url> link was never fetched, and the reader's browser never
    // contacted the site.
    expect(web.hits.map((h) => h.path)).toEqual(['/article?id=1']);
    expect(errors).toEqual([]);
  } finally {
    await web.close();
    await hooks.dispose();
  }
});
