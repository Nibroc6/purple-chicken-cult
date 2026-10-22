// P1-W02 end to end: handshake protocol v2 through the real binary.
//  1. alice (a genesis account with a passkey) shows her code: frames rendered
//     by her device from the frame key. The invitee's browser (a second
//     Chromium whose fake camera films the frame, with a virtual
//     authenticator) claims it with its own keys; both screens show the SAS;
//     alice confirms; the invitee plays the sound and alice's device hears
//     both layers through the AudioInput seam: Verified. The invitee
//     registers, creates a passkey and signs the Invite and Friendship drafts
//     with it, alice co-signs the Invite in the co-sign dialog: friend+ and
//     "invited by @alice", on probation under the friend+ rule.
//  2. A first attempt nobody heard is a retry with the volume hint on both
//     screens; a second one where only the jingle got through is Accepted: a
//     plain friend.
//  3. A photographed QR claimed without e_v, and join calls without the
//     device's signature, are the uniform 404.
// Selectors avoid English text so the spec runs in en and in en-XA.
import { expect, request as pwRequest, test, type BrowserContext, type Page } from '@playwright/test';
import { addVirtualAuthenticator } from './fixtures/auth';
import { installListener, installPlayer, launchWithCamera, relaySound } from './fixtures/camera';
import { World } from './fixtures/world';

const sameOrigin = { 'Sec-Fetch-Site': 'same-origin' };
let origin = '';

test.beforeEach(({ baseURL }) => {
  origin = baseURL ?? '';
});

async function world(): Promise<World> {
  return new World(await pwRequest.newContext({ baseURL: origin }), origin);
}

// enroll makes handle a genesis account with a passkey in page.
async function enroll(page: Page, context: BrowserContext, w: World, handle: string): Promise<void> {
  await addVirtualAuthenticator(context, page);
  const token = await w.genesisToken(handle);
  await page.goto(`${origin}/enroll#${token}`);
  await page.locator('input[name=tos]').check();
  await page.locator('input[name=age]').check();
  await page.getByTestId('enroll-create').click();
  await page.getByTestId('second-done').click();
  await expect(page).toHaveURL(`${origin}/`);
}

// showCode opens alice's invite code and returns the frame the server rendered
// (the one the device syncs to; the test server's clock stands still).
async function showCode(page: Page): Promise<string> {
  await page.goto(`${origin}/invite`);
  await expect(page.getByTestId('invite-available')).toBeVisible();
  const frame = page.waitForResponse((r) => /\/api\/invites\/handshakes\/[^/]+\/frame$/.test(r.url()) && r.ok());
  await page.getByTestId('invite-open').click();
  const { payload } = (await (await frame).json()) as { payload: string };
  expect(payload).toMatch(/^pcc2\./);
  await expect(page.getByTestId('invite-qr')).toBeVisible();
  return payload;
}

async function friends(page: Page): Promise<{ handle: string; relation: string }[]> {
  const res = await page.request.get(`${origin}/api/friends`, { headers: sameOrigin });
  return ((await res.json()) as { items: { handle: string; relation: string }[] }).items;
}

// register fills the join form and signs the drafts with the new passkey;
// alice co-signs the Invite in her dialog meanwhile.
async function register(invitee: Page, inviter: Page, handle: string): Promise<void> {
  await invitee.locator('input[name=handle]').fill(handle);
  await invitee.locator('input[name=display_name]').fill(handle.toUpperCase());
  await invitee.locator('input[name=tos]').check();
  await invitee.locator('input[name=age]').check();
  await invitee.getByTestId('join-submit').click();
  // The signup ceremony shows the drafts the new passkey signs, under Advanced.
  const ceremony = invitee.getByTestId('join-ceremony');
  await expect(ceremony.getByTestId('cosign-record')).toHaveCount(2);
  await ceremony.locator('[data-testid=cosign-advanced] > summary').click();
  await expect(ceremony.locator('[data-kind=invite]')).toBeVisible();
  await expect(ceremony.locator('[data-kind=friendship]')).toBeVisible();
  await invitee.getByTestId('join-sign').click();
  // alice's co-sign dialog opens by itself once the records are drafted.
  const dialog = inviter.getByTestId('cosign-dialog');
  await expect(dialog.getByTestId('cosign-record')).toHaveAttribute('data-kind', 'invite');
  await dialog.getByTestId('cosign-sign').click();
  await expect(invitee.getByTestId('join-invited-by')).toContainText(/@~*alice/, { timeout: 20_000 });
  await invitee.getByTestId('second-done').click();
  await expect(invitee).toHaveURL(`${origin}/`);
}

test('verified in person: QR frames, keys, SAS, both sound layers, both co-signatures ⇒ friend+', async ({
  page,
  context,
  locale,
}) => {
  test.setTimeout(90_000);
  const w = await world();
  await w.reset();
  await w.seed('p1-w02');
  await installListener(page);
  await enroll(page, context, w, 'alice');
  const payload = await showCode(page);

  const cam = await launchWithCamera(payload);
  try {
    const ctx = await cam.browser.newContext({ baseURL: origin, ...(locale ? { locale } : {}) });
    const invitee = await ctx.newPage();
    await addVirtualAuthenticator(ctx, invitee);
    await installPlayer(invitee);
    await invitee.goto(`${origin}/join`);
    await invitee.getByTestId('join-scan').click();
    const sas = invitee.getByTestId('join-sas');
    await expect(sas).toHaveText(/^[0-9]{6}$/, { timeout: 20_000 });
    await expect(page.getByTestId('invite-sas')).toHaveText((await sas.textContent()) ?? '');
    await page.getByTestId('invite-match').click();

    // The sound: alice's device listens, the invitee plays, both layers arrive.
    await expect(page.getByTestId('invite-listening')).toBeVisible();
    await invitee.getByTestId('join-play').click();
    const bits = await relaySound(invitee, page, { ultra: true, jingle: true });
    expect(bits.ultra).toHaveLength(4);
    expect(bits.jingle).toHaveLength(3);
    await expect(page.getByTestId('invite-outcome')).toHaveAttribute('data-outcome', 'verified');

    await register(invitee, page, 'newbie');
    const me = (await (await invitee.request.get(`${origin}/api/session`, { headers: sameOrigin })).json()) as {
      me?: { handle: string; inviter: { handle?: string } };
    };
    expect(me.me?.handle).toBe('newbie');
    expect(me.me?.inviter.handle).toBe('alice');
    // On probation under the Phase 1 rule: friend+ edges outside the invite tree.
    const budget = (await (await invitee.request.get(`${origin}/api/invites/budget`, { headers: sameOrigin })).json()) as {
      probation?: { friend_plus?: boolean; need_outside_friends: number };
    };
    expect(budget.probation).toMatchObject({ friend_plus: true, need_outside_friends: 2 });
  } finally {
    await cam.close();
  }

  await expect(page.getByTestId('invite-done')).toBeVisible();
  expect(await friends(page)).toContainEqual(expect.objectContaining({ handle: 'newbie', relation: 'friend_plus' }));
  await page.goto(`${origin}/invite/people`);
  const card = page.getByTestId('person-card').filter({ hasText: '@newbie' });
  await expect(card.getByTestId('invitee-probation')).toBeVisible();
});

test('a retry shows the volume hint; the jingle alone is Accepted ⇒ a plain friend', async ({ page, context, locale }) => {
  test.setTimeout(90_000);
  const w = await world();
  await w.reset();
  await installListener(page);
  await enroll(page, context, w, 'alice');
  const payload = await showCode(page);
  const cam = await launchWithCamera(payload);
  try {
    const ctx = await cam.browser.newContext({ baseURL: origin, ...(locale ? { locale } : {}) });
    const invitee = await ctx.newPage();
    await addVirtualAuthenticator(ctx, invitee);
    await installPlayer(invitee);
    await invitee.goto(`${origin}/join`);
    await invitee.getByTestId('join-scan').click();
    await expect(invitee.getByTestId('join-sas')).toHaveText(/^[0-9]{6}$/, { timeout: 20_000 });
    await page.getByTestId('invite-match').click();

    await invitee.getByTestId('join-play').click();
    await relaySound(invitee, page, { ultra: false, jingle: false });
    await expect(page.getByTestId('invite-outcome')).toHaveAttribute('data-outcome', 'retry');
    await expect(invitee.getByTestId('join-outcome')).toHaveAttribute('data-outcome', 'retry');

    await invitee.getByTestId('join-play').click();
    await relaySound(invitee, page, { ultra: false, jingle: true });
    await expect(page.getByTestId('invite-outcome')).toHaveAttribute('data-outcome', 'accepted');
    await register(invitee, page, 'plain');
  } finally {
    await cam.close();
  }
  await expect(page.getByTestId('invite-done')).toBeVisible();
  expect(await friends(page)).toContainEqual(expect.objectContaining({ handle: 'plain', relation: 'friend' }));
});

test('a photographed QR without e_v and unsigned join calls are the uniform 404', async ({ page }) => {
  const w = await world();
  await w.reset();
  await w.seed('p1-w02');
  const csrf = await new World(page.request, origin).login('bob');
  const headers = { Origin: origin, ...sameOrigin, 'X-PCC-CSRF': csrf };
  const hv = (await (
    await page.request.post(`${origin}/api/invites/handshakes`, { headers, data: { mode: 'qr_audio' } })
  ).json()) as { hs: string };
  // E_i: any valid P-256 point (the server's frame needs one).
  const k = await crypto.subtle.generateKey({ name: 'ECDH', namedCurve: 'P-256' }, true, ['deriveBits']);
  const ei = Buffer.from(await crypto.subtle.exportKey('raw', k.publicKey)).toString('base64url');
  expect((await page.request.post(`${origin}/api/invites/handshakes/${hv.hs}/epub`, { headers, data: { e_i: ei } })).status()).toBe(204);
  const { payload } = (await (
    await page.request.get(`${origin}/api/invites/handshakes/${hv.hs}/frame`, { headers: sameOrigin })
  ).json()) as { payload: string };

  const guest = await pwRequest.newContext({ baseURL: origin });
  const start = (await (
    await guest.post(`${origin}/api/join/start`, { headers: { Origin: origin, ...sameOrigin }, data: {} })
  ).json()) as { csrf: string };
  const gh = { Origin: origin, ...sameOrigin, 'X-PCC-CSRF': start.csrf };
  for (const data of [
    { payload, e_v: '', sig_pub: '', proof: '' },
    { payload, e_v: ei, sig_pub: ei, proof: 'AAAA' },
  ]) {
    const res = await guest.post(`${origin}/api/join/claim`, { headers: gh, data });
    expect(res.status()).toBe(404);
    expect(await res.text()).toBe('{"error":"not_found"}\n');
  }
  const status = await guest.get(`${origin}/api/join/status`, { headers: { ...sameOrigin, 'X-PCC-Join-Sig': 'AAAA' } });
  expect(status.status()).toBe(404);
  expect(await status.text()).toBe('{"error":"not_found"}\n');
  await guest.dispose();
});
