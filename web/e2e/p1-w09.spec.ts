// P1-W09 end to end: token-only themes validated server-side (a low-contrast
// tier header, a font outside the allowlist, a size under the floor and any
// key outside the schema are refused; nobody else sees a reader's theme), the
// theme page saving a preset that then paints every page and survives a
// reload, the high-contrast switch winning over a theme, every preset keeping
// the integrity elements legible on a feed card, and the Explorer format:
// feed cards with a tree thumbnail that opens the tree, the tree as a canvas
// with the post in focus in full below it (selecting a node brings it into
// focus), zoom and pan by keyboard and pointer, and the minimap only on
// desktop and only zoomed out. Selectors avoid English text, so the spec runs
// in en and en-XA.
import fs from 'node:fs';
import path from 'node:path';
import type { Locator, Page } from '@playwright/test';
import { actor, expect, seedWorld, signInPage, test, type Actor } from './journeys/helpers';

interface Tokens {
  colors: Record<string, string>;
  [k: string]: unknown;
}
const presets = (
  JSON.parse(fs.readFileSync(path.join(import.meta.dirname, '../src/features/themes/presets.json'), 'utf8')) as {
    presets: { id: string; tokens: Tokens }[];
  }
).presets;
const preset = (id: string): Tokens => {
  const p = presets.find((x) => x.id === id);
  if (!p) throw new Error(`no preset ${id}`);
  return p.tokens;
};

const seven = [
  'integrity-tier-header',
  'integrity-chain',
  'integrity-inviter-label',
  'integrity-why',
  'integrity-audience',
  'integrity-edited',
  'integrity-report',
] as const;

// legibility: font size and text contrast against the element's own opaque
// background (integrity elements paint one), else the nearest painted one.
function legibility(el: Locator): Promise<{ fontPx: number; contrast: number }> {
  return el.evaluate((node: HTMLElement) => {
    const ctx = document.createElement('canvas').getContext('2d', { willReadFrequently: true });
    if (!ctx) throw new Error('no canvas');
    const rgba = (c: string) => {
      ctx.clearRect(0, 0, 1, 1);
      ctx.fillStyle = '#000';
      ctx.fillStyle = c;
      ctx.fillRect(0, 0, 1, 1);
      return Array.from(ctx.getImageData(0, 0, 1, 1).data);
    };
    let bg = [255, 255, 255, 255];
    for (let e: Element | null = node; e; e = e.parentElement) {
      const c = rgba(getComputedStyle(e).backgroundColor);
      if ((c[3] ?? 0) === 255) {
        bg = c;
        break;
      }
    }
    const lum = (c: number[]) => {
      const ch = c.slice(0, 3).map((v) => {
        const s = v / 255;
        return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
      });
      return 0.2126 * (ch[0] ?? 0) + 0.7152 * (ch[1] ?? 0) + 0.0722 * (ch[2] ?? 0);
    };
    const cs = getComputedStyle(node);
    const a = lum(rgba(cs.color));
    const b = lum(bg);
    return { fontPx: parseFloat(cs.fontSize), contrast: (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05) };
  });
}

const rootVar = (page: Page, name: string) => page.evaluate((n) => getComputedStyle(document.documentElement).getPropertyValue(n).trim(), name);

async function setup(page: Page, request: Parameters<typeof seedWorld>[0], origin: string): Promise<Actor> {
  await seedWorld(request, origin, 'p1-w09');
  return signInPage(page, origin, 'alice');
}

test('the theme API validates tokens server-side and keeps a theme to its owner', async ({ page, request, baseURL, playwright }) => {
  const origin = baseURL ?? '';
  const alice = await setup(page, request, origin);
  expect(await alice.get('/me/theme')).toEqual({});

  const paper = preset('paper');
  const put = await alice.send('PUT', '/me/theme', { tokens: { ...paper, colors: { ...paper.colors, bg: '#F7F3EA' } } });
  expect(put.status).toBe(200);
  const stored = (JSON.parse(put.body) as { theme: { tokens: Tokens } }).theme.tokens;
  expect(stored.colors.bg).toBe('#f7f3ea');
  expect((await alice.get<{ theme: { tokens: Tokens } }>('/me/theme')).theme.tokens).toEqual(stored);

  for (const [tokens, error, field] of [
    [{ ...paper, colors: { ...paper.colors, integrity_text: '#d9cdb4' } }, 'theme_contrast', 'colors.integrity_text'],
    [{ ...paper, font: 'Comic Sans MS' }, 'theme_font', 'font'],
    [{ ...paper, font_size: 11 }, 'theme_invalid', 'font_size'],
    [{ ...paper, css: '.integrity{display:none}' }, 'theme_invalid', 'tokens'],
    [{ ...paper, colors: { ...paper.colors, border: 'red;display:none' } }, 'theme_invalid', 'colors.border'],
  ] as const) {
    const res = await alice.send('PUT', '/me/theme', { tokens });
    expect(res.status, error).toBe(422);
    expect(JSON.parse(res.body)).toEqual({ error, field });
  }
  // Refusals leave the stored theme alone; nobody else sees it.
  expect((await alice.get<{ theme: { tokens: Tokens } }>('/me/theme')).theme.tokens).toEqual(stored);
  const bob = await actor(playwright, origin, 'bob');
  expect(await bob.get('/me/theme')).toEqual({});

  expect((await alice.send('DELETE', '/me/theme')).status).toBe(204);
  expect(await alice.get('/me/theme')).toEqual({});
  await page.context().clearCookies();
  expect((await alice.raw('/me/theme')).status).toBe(404);
});

test('the theme page saves a preset that paints every page, under the high-contrast switch', async ({ page, request, baseURL }) => {
  const alice = await setup(page, request, baseURL ?? '');
  await page.goto('/settings/theme');
  await expect(page.getByTestId('theme-form')).toHaveAttribute('data-choice', 'default');
  await page.locator('[data-testid="theme-preset"][data-preset="forest"]').check();
  await expect(page.getByTestId('theme-preview')).toBeVisible();
  await page.getByTestId('theme-save').click();
  await expect(page.getByTestId('theme-saved')).toBeVisible();
  const forest = preset('forest');
  await expect.poll(() => rootVar(page, '--color-bg')).toBe(forest.colors.bg);

  await page.goto('/');
  await expect(page.getByTestId('feed-layout')).toBeVisible();
  await expect.poll(() => rootVar(page, '--color-integrity-text')).toBe(forest.colors.integrity_text);
  expect(await rootVar(page, '--radius')).toBe('16px');
  await page.reload();
  await expect.poll(() => rootVar(page, '--color-bg')).toBe(forest.colors.bg);

  // High contrast wins over the theme's colors (its shape stays).
  expect((await alice.send('PATCH', '/me/settings', { ui_prefs: { a11y: { contrast: 'high' } } })).status).toBe(200);
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-contrast', 'high');
  await expect.poll(() => page.evaluate(() => document.documentElement.style.getPropertyValue('--color-bg'))).toBe('');
  expect(await rootVar(page, '--radius')).toBe('16px');

  // The page reopens on the saved preset.
  await page.goto('/settings/theme');
  await expect(page.getByTestId('theme-form')).toHaveAttribute('data-choice', 'forest');
});

test('a theme with a low-contrast tier header is refused in the page', async ({ page, request, baseURL }) => {
  const alice = await setup(page, request, baseURL ?? '');
  await page.goto('/settings/theme');
  await page.locator('[data-testid="theme-preset"][data-preset="paper"]').check();
  await page.getByTestId('theme-color-integrity_text').fill('#d9cdb4');
  await expect(page.getByTestId('theme-form')).toHaveAttribute('data-choice', 'custom');
  await expect(page.locator('[data-testid="theme-check"][data-check="integrity"]')).toHaveAttribute('data-ok', 'false');
  await page.getByTestId('theme-save').click();
  await expect(page.getByTestId('theme-error')).toHaveAttribute('data-code', 'theme_contrast');
  await expect(page.locator('[data-testid="theme-check"][data-check="integrity"]')).toHaveAttribute('data-failed', 'true');
  expect(await alice.get('/me/theme')).toEqual({});
});

test('every preset keeps the integrity elements legible on a feed card', async ({ page, request, baseURL, playwright }) => {
  test.setTimeout(90_000);
  const origin = baseURL ?? '';
  const alice = await setup(page, request, origin);
  // bob edits his post and limits its audience: every element has something to show.
  const bob = await actor(playwright, origin, 'bob');
  const created = await bob.send('POST', '/posts', { body: 'Bob plants beans', lang: 'en', audience: 'd2' });
  expect(created.status).toBe(201);
  const ref = (JSON.parse(created.body) as { ref: string }).ref;
  expect((await bob.send('PATCH', `/posts/${ref}`, { body: 'Bob plants beans and peas' })).status).toBe(200);
  const post = await alice.feedPost('Bob plants beans and peas');

  for (const p of presets) {
    expect((await alice.send('PUT', '/me/theme', { tokens: p.tokens })).status, p.id).toBe(200);
    await page.goto('/');
    await expect.poll(() => rootVar(page, '--color-bg'), p.id).toBe(p.tokens.colors.bg);
    const card = page.locator(`[data-testid="feed-post"][data-ref="${post.ref}"]`);
    await expect(card).toBeVisible();
    const scope = (id: string) => (id === 'integrity-tier-header' ? page.getByTestId('feed-tier').first() : card);
    for (const id of seven) {
      const el = scope(id).getByTestId(id).first();
      await expect(el, `${p.id}: ${id}`).toBeVisible();
      const m = await legibility(el);
      expect(m.fontPx, `${p.id}: ${id} size`).toBeGreaterThanOrEqual(12);
      expect(m.contrast, `${p.id}: ${id} contrast`).toBeGreaterThanOrEqual(4.5);
    }
  }
});

test('Explorer: feed cards with a tree thumbnail, the tree as a canvas with the post in focus below', async ({ page, request, baseURL }) => {
  const alice = await setup(page, request, baseURL ?? '');
  expect((await alice.send('PATCH', '/me/settings', { ui_prefs: { format: 'explorer' } })).status).toBe(200);
  const root = await alice.feedPost('Explorer root by bob');
  const r1 = await alice.feedPost('First reply by bob');
  const r2 = await alice.feedPost('Second reply by carol');

  await page.goto('/');
  await expect(page.getByTestId('feed-layout')).toHaveAttribute('data-format', 'explorer');
  const card = page.locator(`[data-testid="explorer-card"][data-ref="${root.ref}"]`);
  for (const id of seven.slice(1)) await expect(card.getByTestId(id).first(), id).toBeAttached();
  const thumb = card.getByTestId('explorer-thumb');
  await expect(thumb).toHaveAttribute('data-replies', String(root.reply_count));
  await thumb.click();
  await expect(page).toHaveURL(new RegExp(`/p/${root.ref}$`));

  await expect(page.getByTestId('tree-layout')).toHaveAttribute('data-format', 'explorer');
  const nodes = page.getByTestId('explorer-node');
  await expect(nodes.first()).toBeVisible();
  await expect(page.locator(`[data-testid="explorer-node"][data-ref="${root.ref}"]`)).toHaveAttribute('data-focal', 'true');
  // The post in focus carries its integrity elements (the edited mark of an
  // unedited post stays in the DOM, hidden).
  const focal = page.getByTestId('tree-focus');
  for (const id of seven.slice(1)) {
    const el = focal.getByTestId(id).first();
    if (id === 'integrity-edited') await expect(el, id).toBeAttached();
    else await expect(el, id).toBeVisible();
  }

  // Selecting the second reply brings it into focus: its own reply hangs below it.
  const second = page.locator('[data-testid="explorer-node"][data-gen="1"]').nth(1);
  const other = (await second.getAttribute('data-ref')) === r1.ref ? r1 : r2;
  const otherChild = other.ref === r1.ref ? 'Under the first reply' : 'Under the second reply';
  await second.dispatchEvent('click');
  await expect(page).toHaveURL(new RegExp(`/p/${other.ref}$`));
  await expect(page.locator(`[data-testid="explorer-node"][data-ref="${other.ref}"]`)).toHaveAttribute('data-focal', 'true');
  await expect(page.locator('[data-testid="explorer-node"][data-gen="1"]').filter({ hasText: otherChild })).toHaveCount(1);
  await expect(page.locator(`[data-testid="explorer-node"][data-ref="${root.ref}"]`)).toHaveAttribute('data-gen', '-1');

  // Zoom and pan by keyboard and pointer.
  const canvas = page.getByTestId('explorer-canvas');
  const svg = page.getByTestId('explorer-svg');
  await svg.focus();
  await page.keyboard.press('+');
  await expect(canvas).toHaveAttribute('data-zoom', '1.25');
  const before = await svg.getAttribute('viewBox');
  await page.keyboard.press('ArrowLeft');
  await expect(svg).not.toHaveAttribute('viewBox', before ?? '');
  const box = await svg.boundingBox();
  if (!box) throw new Error('no canvas box');
  const panned = await svg.getAttribute('viewBox');
  await page.mouse.move(box.x + 20, box.y + box.height - 20);
  await page.mouse.down();
  await page.mouse.move(box.x + 120, box.y + box.height - 60, { steps: 5 });
  await page.mouse.up();
  await expect(svg).not.toHaveAttribute('viewBox', panned ?? '');

  // On desktop, zooming out shows the minimap; zooming back in hides it.
  await expect(page.getByTestId('explorer-minimap')).toHaveCount(0);
  await page.getByTestId('explorer-zoom-out').click();
  await page.getByTestId('explorer-zoom-out').click();
  await expect(page.getByTestId('explorer-minimap')).toBeVisible();
  await page.getByTestId('explorer-fit').click();
  await page.getByTestId('explorer-zoom-in').click();
  await page.getByTestId('explorer-zoom-in').click();
  await page.getByTestId('explorer-zoom-in').click();
  await expect(page.getByTestId('explorer-minimap')).toHaveCount(0);

  // No minimap on a phone.
  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  await expect(page.getByTestId('explorer-canvas')).toBeVisible();
  await page.getByTestId('explorer-zoom-out').click();
  await page.getByTestId('explorer-zoom-out').click();
  await expect(page.getByTestId('explorer-minimap')).toHaveCount(0);
});
