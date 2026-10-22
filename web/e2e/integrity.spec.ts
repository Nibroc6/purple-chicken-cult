// INT-0 integrity pass (ARCHITECTURE §15.5, plan §6.1 / §7): in every Phase 0
// format (Cards, Compact), in the light and the dark color scheme and with the
// high-contrast switch, the seven integrity elements — tier header, chain,
// inviter label, "why", audience badge, edited mark, report action — are
// present on a feed card and on the focal post of the tree (the inviter label
// in the "how you're connected" modal the card's chain opens), visible, at least
// 12 px text (buttons at least 24 × 24 px), never truncated, with text contrast
// of at least 4.5:1 (WCAG AA) and button borders of at least 3:1 against what
// is actually painted behind them. jsdom cannot measure any of this, so the
// Vitest suite only asserts presence. Selectors avoid English text, so the spec
// runs in en and in the pseudo-locale en-XA.
import type { Locator, Page } from '@playwright/test';
import { actor, expect, seedWorld, signInPage, test, truncatedIntegrity } from './journeys/helpers';

const seven = [
  'integrity-tier-header',
  'integrity-chain',
  'integrity-inviter-label',
  'integrity-why',
  'integrity-audience',
  'integrity-edited',
  'integrity-report',
] as const;

interface Measure {
  visible: boolean;
  width: number;
  height: number;
  fontPx: number;
  button: boolean;
  text: number; // contrast of the text color against the painted background
  border: number; // contrast of the border color (buttons), 0 when none
}

// measure resolves colors through a canvas (any CSS color syntax becomes sRGB
// bytes) and composites every translucent background up to the root canvas.
function measure(el: Locator): Promise<Measure> {
  return el.evaluate((node: HTMLElement) => {
    const ctx = document.createElement('canvas').getContext('2d', { willReadFrequently: true });
    if (!ctx) throw new Error('no canvas');
    const rgba = (c: string): [number, number, number, number] => {
      ctx.clearRect(0, 0, 1, 1);
      ctx.fillStyle = '#000';
      ctx.fillStyle = c;
      ctx.fillRect(0, 0, 1, 1);
      const d = ctx.getImageData(0, 0, 1, 1).data;
      return [d[0] ?? 0, d[1] ?? 0, d[2] ?? 0, (d[3] ?? 0) / 255];
    };
    const over = (top: number[], below: number[]) => [0, 1, 2].map((i) => (top[i] ?? 0) * (top[3] ?? 1) + (below[i] ?? 0) * (1 - (top[3] ?? 1)));
    // The canvas behind everything: the root's background, else white.
    const rootBg = rgba(getComputedStyle(document.documentElement).backgroundColor);
    let painted = rootBg[3] > 0 ? over(rootBg, [255, 255, 255]) : [255, 255, 255];
    const layers: [number, number, number, number][] = [];
    for (let e: Element | null = node; e && e !== document.documentElement; e = e.parentElement) {
      const bg = rgba(getComputedStyle(e).backgroundColor);
      if (bg[3] > 0) layers.push(bg);
      if (bg[3] >= 1) break;
    }
    for (const l of layers.reverse()) painted = over(l, painted);
    const lum = (c: number[]) => {
      const ch = c.slice(0, 3).map((v) => {
        const s = v / 255;
        return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
      });
      return 0.2126 * (ch[0] ?? 0) + 0.7152 * (ch[1] ?? 0) + 0.0722 * (ch[2] ?? 0);
    };
    const contrast = (fg: [number, number, number, number]) => {
      const a = lum(over(fg, painted));
      const b = lum(painted);
      return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
    };
    const cs = getComputedStyle(node);
    const box = node.getBoundingClientRect();
    const button = node.tagName === 'BUTTON';
    return {
      visible: node.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true }),
      width: box.width,
      height: box.height,
      fontPx: parseFloat(cs.fontSize),
      button,
      text: contrast(rgba(cs.color)),
      border: button && parseFloat(cs.borderTopWidth) > 0 ? contrast(rgba(cs.borderTopColor)) : 0,
    };
  });
}

async function expectLegible(scope: Locator, id: string, what: string): Promise<void> {
  const el = scope.getByTestId(id).first();
  await expect(el, `${what}: ${id}`).toBeVisible();
  const m = await measure(el);
  expect(m.visible, `${what}: ${id} visible`).toBe(true);
  expect(m.fontPx, `${what}: ${id} font size`).toBeGreaterThanOrEqual(12);
  expect(m.height, `${what}: ${id} height`).toBeGreaterThanOrEqual(m.button ? 24 : 12);
  expect(m.width, `${what}: ${id} width`).toBeGreaterThanOrEqual(m.button ? 24 : 12);
  expect(m.text, `${what}: ${id} text contrast`).toBeGreaterThanOrEqual(4.5);
  if (m.button) expect(m.border, `${what}: ${id} border contrast`).toBeGreaterThanOrEqual(3);
}

async function checkPage(page: Page, card: Locator, header: Locator, what: string): Promise<void> {
  await expect(card).toBeVisible();
  await expectLegible(header, 'integrity-tier-header', what);
  for (const id of seven.slice(1)) if (id !== 'integrity-inviter-label') await expectLegible(card, id, what);
  expect(await truncatedIntegrity(page), `${what}: truncated`).toEqual([]);
}

for (const format of ['cards', 'compact'] as const) {
  test(`the seven integrity elements are visible and legible in ${format}`, async ({ page, request, baseURL, playwright }) => {
    test.setTimeout(90_000);
    const origin = baseURL ?? '';
    await seedWorld(request, origin, 'int-0');
    // bob edits his post and limits its audience: every element has something to show.
    const bob = await actor(playwright, origin, 'bob');
    const created = await bob.send('POST', '/posts', { body: 'Bob plants tomatoes', lang: 'en', audience: 'd2' });
    expect(created.status).toBe(201);
    const ref = (JSON.parse(created.body) as { ref: string }).ref;
    expect((await bob.send('PATCH', `/posts/${ref}`, { body: 'Bob plants tomatoes and beans' })).status).toBe(200);

    const alice = await signInPage(page, origin, 'alice');
    expect((await alice.send('PATCH', '/me/settings', { ui_prefs: { format } })).status).toBe(200);
    const post = await alice.feedPost('Bob plants tomatoes and beans');
    for (const scheme of ['light', 'dark'] as const) {
      for (const contrast of ['normal', 'high'] as const) {
        const what = `${format}/${scheme}/${contrast}`;
        expect((await alice.send('PATCH', '/me/settings', { ui_prefs: { format, a11y: { contrast } } })).status).toBe(200);
        await page.emulateMedia({ colorScheme: scheme });

        // Feed: the card and the header of its tier.
        await page.goto('/');
        await expect(page.getByTestId('feed-layout')).toHaveAttribute('data-format', format);
        await expect(page.locator('html')).toHaveAttribute('data-contrast', contrast);
        const card = page.locator(`[data-testid="feed-post"][data-ref="${post.ref}"]`);
        await checkPage(page, card, page.getByTestId('feed-tier').first(), `${what} feed`);

        // Tree: the post in focus (the tree carries no tier header, so the
        // feed's header stands for the element there).
        await page.goto(`/p/${post.ref}`);
        await expect(page.getByTestId('tree-layout')).toHaveAttribute('data-format', format);
        const focal = page.getByTestId('tree-focus');
        for (const id of seven.slice(2)) if (id !== 'integrity-inviter-label') await expectLegible(focal, id, `${what} tree`);
        await expectLegible(focal, 'integrity-chain', `${what} tree`);
        expect(await truncatedIntegrity(page), `${what} tree: truncated`).toEqual([]);

        // The inviter label and its own "report the inviter" action, one tap
        // from the chain (opened here: a feed card left on screen behind the
        // modal would count as seen and leave the next feed session).
        await focal.getByTestId('integrity-chain-open').first().click();
        const via = page.getByTestId('via-modal');
        await expectLegible(via, 'integrity-inviter-label', `${what} via`);
        await expectLegible(via, 'integrity-inviter-report', `${what} via`);
        expect(await truncatedIntegrity(page), `${what} via: truncated`).toEqual([]);
        await page.keyboard.press('Escape');
        await expect(via).toHaveCount(0);
      }
    }
  });
}
