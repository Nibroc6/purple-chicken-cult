// P0-W04 end to end: alice shows her rotating invite QR; the invitee's
// browser (a second Chromium whose fake camera films that QR, plus a virtual
// authenticator) scans it; both screens show the same SAS; alice confirms; the
// invitee registers with a passkey and is "invited by @alice" and friends with
// her; alice sees the new invitee on probation. Selectors avoid English text so
// the spec runs in en and in the pseudo-locale en-XA.
import { expect, request as pwRequest, test } from '@playwright/test';
import { addVirtualAuthenticator } from './fixtures/auth';
import { launchWithCamera } from './fixtures/camera';
import { World } from './fixtures/world';

test('invite in person: QR scan, SAS confirm, passkey registration', async ({ page, baseURL, locale }) => {
  const origin = baseURL ?? '';
  const hooks = await pwRequest.newContext({ baseURL: origin });
  const world = new World(hooks, origin);
  await world.reset();
  await world.seed('p0-w04');

  // alice: the invite screen with her saved invites, then the QR code. The
  // test server's clock stands still, so the frame never goes stale.
  await new World(page.request, origin).login('alice');
  await page.goto(`${origin}/invite`);
  await expect(page.getByTestId('invite-available')).toBeVisible();
  const frame = page.waitForResponse((r) => /\/api\/invites\/handshakes\/[^/]+\/frame$/.test(r.url()) && r.ok());
  await page.getByTestId('invite-open').click();
  const { payload } = (await (await frame).json()) as { payload: string };
  await expect(page.getByTestId('invite-qr')).toBeVisible();

  // The invitee scans it with a real camera pipeline (fake device).
  const cam = await launchWithCamera(payload);
  try {
    const ctx = await cam.browser.newContext({ baseURL: origin, ...(locale ? { locale } : {}) });
    const invitee = await ctx.newPage();
    await addVirtualAuthenticator(ctx, invitee);
    await invitee.goto(`${origin}/join`);
    await invitee.getByTestId('join-scan').click();
    const sas = invitee.getByTestId('join-sas');
    await expect(sas).toHaveText(/^[0-9]{6}$/, { timeout: 20_000 });
    await expect(page.getByTestId('invite-sas')).toHaveText((await sas.textContent()) ?? '');

    // Registration opens only after "codes match".
    await page.getByTestId('invite-match').click();
    await invitee.locator('input[name=handle]').fill('newbie');
    await invitee.locator('input[name=display_name]').fill('Newbie');
    await invitee.locator('input[name=tos]').check();
    await invitee.locator('input[name=age]').check();
    await invitee.getByTestId('join-submit').click();
    await expect(invitee.getByTestId('join-invited-by')).toContainText('@alice');
    await invitee.getByTestId('second-done').click();
    await expect(invitee).toHaveURL(`${origin}/`);
    const me = (await (
      await invitee.request.get(`${origin}/api/session`, { headers: { 'Sec-Fetch-Site': 'same-origin' } })
    ).json()) as { me?: { handle: string; inviter: { handle?: string } } };
    expect(me.me?.handle).toBe('newbie');
    expect(me.me?.inviter.handle).toBe('alice');
  } finally {
    await cam.close();
  }

  // alice: the handshake is done, newbie is a friend on probation.
  await expect(page.getByTestId('invite-done')).toBeVisible();
  await page.goto(`${origin}/invite/people`);
  const card = page.getByTestId('person-card').filter({ hasText: '@newbie' });
  await expect(card).toBeVisible();
  await expect(card.getByTestId('invitee-probation')).toBeVisible();
  const friends = (await (
    await page.request.get(`${origin}/api/friends`, { headers: { 'Sec-Fetch-Site': 'same-origin' } })
  ).json()) as { items: { handle: string }[] };
  expect(friends.items.map((p) => p.handle)).toContain('newbie');
  await hooks.dispose();
});
