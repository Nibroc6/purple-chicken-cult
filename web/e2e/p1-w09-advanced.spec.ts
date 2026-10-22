// Advanced themes end to end (contract request p1-w09-3): theme CSS is the
// reader's own raw CSS (the server checks size and image() names only), and
// the theme page's advanced mode, once the risks are acknowledged, uploads an
// image, uses it to replace the like button's text, and saves. The feed then
// shows the image button through a constructed stylesheet under the app's
// strict CSP (the fixture fails on any violation), settings pages stay
// unthemed, and high contrast drops the sheet.
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

test('the theme API stores raw theme CSS and checks image names', async ({ page, request, baseURL }) => {
  const alice = await setup(page, request, baseURL ?? '');
  const bad = await alice.send('PUT', '/me/theme', { tokens: { colors: paper }, css: 'a {}\n.brand { background: image(logo) }' });
  expect(bad.status).toBe(422);
  expect(JSON.parse(bad.body)).toEqual({ error: 'theme_css', field: 'css.2' });
  expect(await alice.get('/me/theme')).toEqual({});
  const ok = await alice.send('PUT', '/me/theme', { tokens: { colors: paper }, css: '.post { border: 3px dashed #000 }\r\n' });
  expect(ok.status).toBe(200);
  expect((JSON.parse(ok.body) as { theme: { css: string } }).theme.css).toBe('.post { border: 3px dashed #000 }\n');
});

test('an image like button from the theme page, with settings pages untouched', async ({ page, request, baseURL }) => {
  test.setTimeout(90_000);
  const alice = await setup(page, request, baseURL ?? '');
  const post = await alice.feedPost((await alice.feedPosts())[0]?.body ?? '');

  await page.goto('/settings/theme');
  await page.locator('[data-testid="theme-preset"][data-preset="paper"]').check();
  await page.getByTestId('theme-advanced').locator('summary').first().click();
  await expect(page.getByTestId('theme-css-warning')).toBeVisible();
  await expect(page.getByTestId('theme-css')).toBeDisabled();
  await page.getByTestId('theme-css-ack').check();
  await page.getByTestId('theme-image-name').fill('heart');
  await page.getByTestId('theme-image-file').setInputFiles({ name: 'heart.png', mimeType: 'image/png', buffer: await heartPNG(page) });
  await page.getByTestId('theme-image-add').click();
  await expect(page.locator('[data-testid="theme-image"][data-name="heart"]')).toBeVisible();
  await page
    .getByTestId('theme-css')
    .fill('[data-part=like] {\n  background: image(heart) center / contain no-repeat;\n  font-size: 0;\n  min-inline-size: 48px;\n  min-block-size: 48px;\n}\nbutton { border-width: 3px; border-style: dashed }');
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
  // Other buttons get the rule too.
  expect(await card.getByTestId('engage-save').evaluate((b) => getComputedStyle(b).borderTopWidth)).toBe('3px');

  // High contrast drops the sheet.
  expect((await alice.send('PATCH', '/me/settings', { ui_prefs: { a11y: { contrast: 'high' } } })).status).toBe(200);
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-contrast', 'high');
  await expect.poll(() => card.getByTestId('engage-like').evaluate((b) => getComputedStyle(b).backgroundImage)).toBe('none');
});
