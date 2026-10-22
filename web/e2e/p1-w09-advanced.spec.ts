// Advanced themes end to end (contract request p1-w09-3): theme CSS is a
// closed dialect the server refuses outside of (url(), class selectors,
// hiding, unreadable text), and the theme page's advanced mode uploads an
// image, uses it to replace the like button's text, and saves. The feed then
// shows the image button through a constructed stylesheet under the app's
// strict CSP (the fixture fails on any violation), while integrity elements
// keep their own styles and legibility, and settings pages stay unthemed.
// Selectors avoid English text, so the spec runs in en and en-XA.
import type { Page } from '@playwright/test';
import { expect, seedWorld, signInPage, test, type Actor } from './journeys/helpers';

async function setup(page: Page, request: Parameters<typeof seedWorld>[0], origin: string): Promise<Actor> {
  await seedWorld(request, origin, 'p1-w09');
  return signInPage(page, origin, 'alice');
}

const paper = { bg: '#f7f3ea', surface: '#fffdf8', text: '#2b2620', muted: '#5f574b', accent: '#8a3b12', border: '#e4dccb', integrity_bg: '#efe5d0', integrity_text: '#3a2a10' };

// A small opaque PNG drawn in the page.
async function heartPNG(page: Page): Promise<Buffer> {
  const b64 = await page.evaluate(() => {
    const c = document.createElement('canvas');
    c.width = 48;
    c.height = 48;
    const ctx = c.getContext('2d');
    if (!ctx) throw new Error('no canvas');
    ctx.fillStyle = '#c2185b';
    ctx.fillRect(0, 0, 48, 48);
    ctx.fillStyle = '#fce4ec';
    ctx.fillRect(12, 12, 24, 24);
    return c.toDataURL('image/png').split(',')[1] ?? '';
  });
  return Buffer.from(b64, 'base64');
}

test('the theme API refuses anything outside theme CSS', async ({ page, request, baseURL }) => {
  const alice = await setup(page, request, baseURL ?? '');
  for (const [css, error, field] of [
    ['card {\n  background-image: url(https://evil.example/x.png);\n}', 'theme_css', 'css.2'],
    ['.integrity { display: none }', 'theme_css', 'css.1'],
    ['card { display: none }', 'theme_css', 'css.1'],
    ['@import url(x);', 'theme_css', 'css.1'],
    ['like { label: hidden }', 'theme_css', 'css.1'],
    ['card { color: #f0f0f0 }', 'theme_contrast', 'css.1'],
  ] as const) {
    const res = await alice.send('PUT', '/me/theme', { tokens: { colors: paper }, css });
    expect(res.status, css).toBe(422);
    expect(JSON.parse(res.body), css).toEqual({ error, field });
  }
  expect(await alice.get('/me/theme')).toEqual({});
  const ok = await alice.send('PUT', '/me/theme', { tokens: { colors: paper }, css: 'Button{Border-Width:3px;border-style:dashed}' });
  expect(ok.status).toBe(200);
  expect((JSON.parse(ok.body) as { theme: { css: string } }).theme.css).toBe('button {\n  border-width: 3px;\n  border-style: dashed;\n}\n');
});

test('an image like button from the theme page, with integrity elements and settings untouched', async ({ page, request, baseURL }) => {
  test.setTimeout(90_000);
  const alice = await setup(page, request, baseURL ?? '');
  const post = await alice.feedPost((await alice.feedPosts())[0]?.body ?? '');

  await page.goto('/settings/theme');
  await page.locator('[data-testid="theme-preset"][data-preset="paper"]').check();
  await page.getByTestId('theme-advanced').locator('summary').first().click();
  await page.getByTestId('theme-image-name').fill('heart');
  await page.getByTestId('theme-image-file').setInputFiles({ name: 'heart.png', mimeType: 'image/png', buffer: await heartPNG(page) });
  await page.getByTestId('theme-image-add').click();
  await expect(page.locator('[data-testid="theme-image"][data-name="heart"]')).toBeVisible();
  await page.getByTestId('theme-css').fill('like {\n  background-image: image(heart);\n  label: hidden;\n  min-inline-size: 48px;\n  min-block-size: 48px;\n}\nbutton { border-width: 3px; border-style: dashed }');
  await expect(page.getByTestId('theme-css-status')).toHaveAttribute('data-ok', 'true');
  // The preview shows it before saving.
  const previewLike = page.getByTestId('theme-preview').locator('[data-part="like"]');
  await expect.poll(() => previewLike.evaluate((b) => getComputedStyle(b).backgroundImage)).toContain('url(');
  await page.getByTestId('theme-save').click();
  await expect(page.getByTestId('theme-saved')).toBeVisible();
  // Settings pages are locked: the sheet is not applied there.
  const saveButton = page.getByTestId('theme-save');
  expect(await saveButton.evaluate((b) => getComputedStyle(b).borderTopWidth)).not.toBe('3px');

  await page.goto('/');
  const card = page.locator(`[data-testid="feed-post"][data-ref="${post.ref}"]`);
  await expect(card).toBeVisible();
  const like = card.getByTestId('engage-like');
  await expect.poll(() => like.evaluate((b) => getComputedStyle(b).backgroundImage)).toMatch(/^url\(/);
  const style = await like.evaluate((b) => {
    const cs = getComputedStyle(b);
    return { font: cs.fontSize, w: b.getBoundingClientRect().width, label: b.textContent };
  });
  expect(style.font).toBe('0px');
  expect(style.w).toBeGreaterThanOrEqual(48);
  expect(style.label).not.toBe(''); // the accessible name stays the text
  // The image itself loads from the media origin.
  const src = (await like.evaluate((b) => getComputedStyle(b).backgroundImage)).replace(/^url\("?|"?\)$/g, '');
  expect((await page.request.get(src)).status()).toBe(200);
  // Other buttons get the rule; the integrity report button keeps its own border.
  expect(await card.getByTestId('engage-save').evaluate((b) => getComputedStyle(b).borderTopWidth)).toBe('3px');
  const report = card.getByTestId('integrity-report').first();
  await expect(report).toBeVisible();
  expect(await report.evaluate((b) => getComputedStyle(b).borderTopStyle)).not.toBe('dashed');

  // High contrast drops the sheet.
  expect((await alice.send('PATCH', '/me/settings', { ui_prefs: { a11y: { contrast: 'high' } } })).status).toBe(200);
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-contrast', 'high');
  await expect.poll(() => card.getByTestId('engage-like').evaluate((b) => getComputedStyle(b).backgroundImage)).toBe('none');
});
