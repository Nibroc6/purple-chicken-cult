// Icons draw whole (lib/icon.css): on the feed, each distinct icon looks the
// same in its button as it does with every clip lifted, so no box, clip path
// or overflow shaves its edge (the side of a heart, the foot of a circle). A
// pixel comparison at 3×, so a one-pixel edge shows; runs in en and en-XA.
import type { Page } from '@playwright/test';
import { actor, expect, seedWorld, signInPage, test } from './journeys/helpers';

// strongDiff counts the pixels that differ by more than a quarter of the range
// in some channel: a shaved edge does, rasterization noise (a few levels) does
// not. The page's canvas decodes the PNGs.
function strongDiff(page: Page, a: Buffer, b: Buffer): Promise<number> {
  return page.evaluate(
    async ([x, y]) => {
      const pixels = async (src: string) => {
        const img = new Image();
        img.src = src;
        await img.decode();
        const c = document.createElement('canvas');
        c.width = img.width;
        c.height = img.height;
        const ctx = c.getContext('2d', { willReadFrequently: true });
        if (!ctx) throw new Error('no canvas');
        ctx.drawImage(img, 0, 0);
        return ctx.getImageData(0, 0, c.width, c.height).data;
      };
      const [p, q] = await Promise.all([pixels(x), pixels(y)]);
      let n = 0;
      for (let i = 0; i < p.length; i += 4) {
        if ([0, 1, 2, 3].some((k) => Math.abs((p[i + k] ?? 0) - (q[i + k] ?? 0)) > 64)) n++;
      }
      return n;
    },
    [`data:image/png;base64,${a.toString('base64')}`, `data:image/png;base64,${b.toString('base64')}`] as const,
  );
}

test.use({ deviceScaleFactor: 3 });

test('every icon on the feed draws whole', async ({ page, request, baseURL, playwright }) => {
  test.setTimeout(90_000);
  const origin = baseURL ?? '';
  await seedWorld(request, origin, 'int-0');
  // bob limits his post's audience, so the card shows every integrity icon.
  const bob = await actor(playwright, origin, 'bob');
  expect((await bob.send('POST', '/posts', { body: 'Bob plants tomatoes', lang: 'en', audience: 'd2' })).status).toBe(201);
  const alice = await signInPage(page, origin, 'alice');
  const post = await alice.feedPost('Bob plants tomatoes');
  await page.goto('/');
  await expect(page.locator(`[data-testid="feed-post"][data-ref="${post.ref}"]`)).toBeVisible();
  await page.mouse.move(0, 0);

  const names = await page.locator('svg.icon').evaluateAll((els) => [...new Set(els.map((e) => e.getAttribute('data-icon') ?? ''))]);
  expect(names.length).toBeGreaterThan(10);
  for (const name of names) {
    const icon = page.locator(`svg.icon[data-icon="${name}"]:visible`).first();
    if ((await icon.count()) === 0) continue;
    await icon.scrollIntoViewIfNeeded();
    // The area around the icon, a quarter em on each side.
    const box = await icon.evaluate((e) => {
      const r = e.getBoundingClientRect();
      const pad = r.height / 4;
      return { x: r.x - pad, y: r.y - pad, width: r.width + 2 * pad, height: r.height + 2 * pad };
    });
    if (box.width === 0) continue;
    const drawn = await page.screenshot({ clip: box, animations: 'disabled' });
    await icon.evaluate((e: SVGElement) => {
      e.style.setProperty('clip-path', 'none', 'important');
      e.style.setProperty('overflow', 'visible', 'important');
    });
    const whole = await page.screenshot({ clip: box, animations: 'disabled' });
    await icon.evaluate((e: SVGElement) => {
      e.style.removeProperty('clip-path');
      e.style.removeProperty('overflow');
    });
    expect.soft(await strongDiff(page, drawn, whole), `icon ${name} is clipped`).toBe(0);
  }
});
