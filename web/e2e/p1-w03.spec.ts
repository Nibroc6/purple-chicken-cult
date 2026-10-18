// P1-W03 end to end, through the real binary:
//  1. Co-signed graph changes: alice and dave (genesis accounts with passkeys)
//     — alice's friend request, dave's acceptance, alice's unfriend and both
//     kinds of block each open the co-sign dialog with the exact record
//     (friendship, revocation, block) and are signed with the passkey; a
//     passkey session's change without a signature is the uniform 404.
//  2. Hide-via: vic hides the paths through his friend hal; hal and hal's
//     posts stay, wes (reachable only through hal) drops out of the feed, xan
//     is now reached around hal; showing the paths again restores them.
//  3. friend_plus posts reach friend+ friends only, never plain friends.
//  4. The friend+ upgrade in person (deferred by P1-W02 until this route): alice
//     opens it on bob's page (POST /friends/{ref}/upgrade); bob's signed-in
//     browser, whose fake camera films alice's frame, scans it at /join; SAS,
//     both sound layers through the AudioInput seam, both co-signatures:
//     friend+.
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

// handle matches "@handle" in en and en-XA (the pseudo-locale pads the literal @).
const at = (handle: string) => new RegExp(`@~*${handle}(?![a-z0-9_])`);

// card is the person card of handle (matched on its name link, not the chain).
const card = (page: Page, handle: string) =>
  page.getByTestId('person-card').filter({ has: page.locator('a.people-name', { hasText: at(handle) }) });

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

// accounts makes sure the fixture's existing accounts exist (active, without a
// passkey) before it is seeded. The login that activates them runs in a
// throwaway context (w's own requests carry no CSRF token).
async function accounts(w: World, handles: string[]): Promise<void> {
  for (const h of handles) {
    await w.genesisToken(h);
    const ctx = await pwRequest.newContext({ baseURL: origin });
    await new World(ctx, origin).login(h);
    await ctx.dispose();
  }
}

// sign signs the open co-sign dialog of page, which must show a record of kind.
async function sign(page: Page, kind: string): Promise<void> {
  const dialog = page.getByTestId('cosign-dialog');
  await expect(dialog.getByTestId('cosign-record')).toHaveAttribute('data-kind', kind);
  await dialog.getByTestId('cosign-sign').click();
  await expect(dialog).toHaveCount(0);
}

interface PersonJSON {
  ref: string;
  handle: string;
  relation: string;
  hidden_via?: boolean;
  chain: { dist: number; first_hop?: { handle: string } };
}
interface PostJSON {
  body: string;
  author: PersonJSON;
}

async function getJSON<T>(page: Page, path: string): Promise<T> {
  const res = await page.request.get(`${origin}/api${path}`, { headers: sameOrigin });
  expect(res.status()).toBe(200);
  return (await res.json()) as T;
}

async function friends(page: Page): Promise<PersonJSON[]> {
  return (await getJSON<{ items: PersonJSON[] }>(page, '/friends')).items;
}

// feed returns the posts of a new feed session's first page.
async function feed(page: Page): Promise<PostJSON[]> {
  const f = await getJSON<{ items: { post?: PostJSON; thread?: PostJSON[] }[] }>(page, '/feed');
  return f.items.flatMap((i) => (i.post ? [i.post] : (i.thread ?? [])));
}

async function loginAs(page: Page, handle: string): Promise<void> {
  await page.context().clearCookies();
  await new World(page.request, origin).login(handle);
}

test('friend request, acceptance, unfriend and blocks are co-signed with the passkey', async ({ page, context, browser, locale }) => {
  test.setTimeout(90_000);
  const w = await world();
  await w.reset();
  await enroll(page, context, w, 'alice');
  const daveCtx = await browser.newContext({ baseURL: origin, ...(locale ? { locale } : {}) });
  const dave = await daveCtx.newPage();
  try {
    await enroll(dave, daveCtx, w, 'dave');
    await accounts(w, ['bob']);
    await w.seed('p1-w03');

    // alice asks dave (a suggestion: carol and erin are mutual friends).
    await page.goto(`${origin}/suggestions`);
    const daveCard = card(page, 'dave');
    await daveCard.getByTestId('action-request').click();
    await sign(page, 'friendship');
    await expect(daveCard.getByTestId('action-withdraw')).toBeVisible();

    // dave accepts, signing the same Friendship record.
    await dave.goto(`${origin}/friends/requests`);
    const incoming = card(dave, 'alice');
    await incoming.getByTestId('action-accept').click();
    await sign(dave, 'friendship');
    await expect(card(dave, 'alice')).toHaveCount(0);
    expect(await friends(dave)).toContainEqual(expect.objectContaining({ handle: 'alice', relation: 'friend' }));

    // A passkey session's change without its co-signature is the uniform 404.
    const session = (await (await page.request.get(`${origin}/api/session`, { headers: sameOrigin })).json()) as { csrf: string };
    const mine = await friends(page);
    const carol = mine.find((p) => p.handle === 'carol');
    expect(carol).toBeDefined();
    const unsigned = await page.request.delete(`${origin}/api/friends/${carol?.ref ?? ''}`, {
      headers: { ...sameOrigin, Origin: origin, 'X-PCC-CSRF': session.csrf },
    });
    expect(unsigned.status()).toBe(404);
    expect(await unsigned.text()).toBe('{"error":"not_found"}\n');
    expect((await friends(page)).map((p) => p.handle)).toContain('carol');

    // alice unfriends bob: the Revocation.
    await page.goto(`${origin}/friends`);
    const bob = card(page, 'bob');
    await bob.getByTestId('action-unfriend').click();
    await bob.getByTestId('confirm-unfriend').click();
    await sign(page, 'revocation');
    await expect(card(page, 'bob')).toHaveCount(0);

    // alice blocks gus (not a friend): the Block record.
    await page.goto(`${origin}/suggestions`);
    await card(page, 'gus').locator('a').first().click();
    await expect(page.getByTestId('person-relation')).toHaveAttribute('data-relation', 'connected');
    await page.getByTestId('action-block').click();
    await page.getByTestId('confirm-block').click();
    await sign(page, 'block');
    await expect(page.getByTestId('action-unblock')).toBeVisible();

    // alice blocks dave, a friend now: the block signs the Revocation it writes.
    await page.goto(`${origin}/friends`);
    await card(page, 'dave').locator('a').first().click();
    await expect(page.getByTestId('person-relation')).toHaveAttribute('data-relation', 'friend');
    await page.getByTestId('action-block').click();
    await page.getByTestId('confirm-block').click();
    await sign(page, 'revocation');
    await expect(page.getByTestId('action-unblock')).toBeVisible();
    expect((await friends(page)).map((p) => p.handle).sort()).toEqual(['carol', 'erin']);

    // Cancelling the dialog changes nothing.
    await page.goto(`${origin}/friends`);
    const erin = card(page, 'erin');
    await erin.getByTestId('action-unfriend').click();
    await erin.getByTestId('confirm-unfriend').click();
    await expect(page.getByTestId('cosign-sign')).toBeVisible();
    await page.getByTestId('cosign-cancel').click();
    await expect(erin.getByTestId('confirm-unfriend')).toBeVisible();
    expect((await friends(page)).map((p) => p.handle)).toContain('erin');
  } finally {
    await daveCtx.close();
  }
});

test('hiding the paths through a friend keeps them and their posts, and drops only the paths', async ({ page }) => {
  const w = await world();
  await w.reset();
  await accounts(w, ['alice', 'bob', 'dave']);
  await w.seed('p1-w03');
  await loginAs(page, 'vic');

  let posts = await feed(page);
  expect(posts.map((p) => p.body)).toEqual(expect.arrayContaining(["Hal's own post", 'Wes behind Hal', 'Xan two ways']));
  expect(posts.find((p) => p.body === 'Xan two ways')?.author.chain).toMatchObject({ dist: 2, first_hop: { handle: 'hal' } });

  await page.goto(`${origin}/friends`);
  await card(page, 'hal').locator('a').first().click();
  await page.getByTestId('action-hide_via').click();
  await page.getByTestId('confirm-hide_via').click();
  await expect(page.getByTestId('person-hidden-via')).toBeVisible();
  await expect(page.getByTestId('person-relation')).toHaveAttribute('data-relation', 'friend');

  posts = await feed(page);
  const bodies = posts.map((p) => p.body);
  expect(bodies).toContain("Hal's own post"); // the hidden friend's own post stays
  expect(bodies).not.toContain('Wes behind Hal'); // wes is reachable only through hal
  expect(posts.find((p) => p.body === 'Xan two ways')?.author.chain).toMatchObject({ dist: 3, first_hop: { handle: 'yan' } });
  expect((await friends(page)).find((p) => p.handle === 'hal')).toMatchObject({ relation: 'friend', hidden_via: true });

  // hal is never told.
  await loginAs(page, 'hal');
  const vic = (await friends(page)).find((p) => p.handle === 'vic');
  expect(vic?.hidden_via).toBeUndefined();

  // Showing the paths again restores wes.
  await loginAs(page, 'vic');
  await page.goto(`${origin}/friends`);
  await card(page, 'hal').locator('a').first().click();
  await page.getByTestId('action-show_via').click();
  await expect(page.getByTestId('person-hidden-via')).toHaveCount(0);
  expect((await feed(page)).map((p) => p.body)).toContain('Wes behind Hal');
});

test('friend_plus posts reach friend+ friends only', async ({ page }) => {
  const w = await world();
  await w.reset();
  await accounts(w, ['alice', 'bob', 'dave']);
  await w.seed('p1-w03');

  await loginAs(page, 'fpf');
  let bodies = (await feed(page)).map((p) => p.body);
  expect(bodies).toEqual(expect.arrayContaining(['Only for friend+', 'For everyone']));
  await page.goto(`${origin}/`);
  await expect(page.getByText('Only for friend+')).toBeVisible();

  await loginAs(page, 'pfr');
  bodies = (await feed(page)).map((p) => p.body);
  expect(bodies).toContain('For everyone');
  expect(bodies).not.toContain('Only for friend+');
  await page.goto(`${origin}/`);
  await expect(page.getByText('For everyone')).toBeVisible();
  await expect(page.getByText('Only for friend+')).toHaveCount(0);
});

// ---- the friend+ upgrade in person ----

interface Credential {
  credentialId: string;
  isResidentCredential: boolean;
  rpId?: string;
  privateKey: string;
  userHandle?: string;
  signCount: number;
}

// authenticator adds a virtual authenticator to page and returns its CDP handle.
async function authenticator(context: BrowserContext, page: Page) {
  const cdp = await context.newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  const { authenticatorId } = await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true },
  });
  return {
    credentials: async () => (await cdp.send('WebAuthn.getCredentials', { authenticatorId })).credentials as Credential[],
    add: async (c: Credential) => {
      await cdp.send('WebAuthn.addCredential', { authenticatorId, credential: c });
    },
  };
}

test('a friend+ upgrade in person: code, SAS, both sound layers and both co-signatures', async ({ page, context, browser, locale }) => {
  test.setTimeout(120_000);
  const w = await world();
  await w.reset();
  await installListener(page);
  await enroll(page, context, w, 'alice');
  // bob enrolls in his own browser; his session and passkey then move to the
  // browser whose camera films alice's code (it must be launched with it).
  const bobCtx = await browser.newContext({ baseURL: origin, ...(locale ? { locale } : {}) });
  const bobPage = await bobCtx.newPage();
  const bobAuth = await authenticator(bobCtx, bobPage);
  // Load the app once first: the enrollment page then starts from a settled session.
  await bobPage.goto(`${origin}/`);
  await bobPage.waitForLoadState('networkidle');
  const token = await w.genesisToken('bob');
  await bobPage.goto(`${origin}/enroll#${token}`);
  await bobPage.locator('input[name=tos]').check();
  await bobPage.locator('input[name=age]').check();
  await bobPage.getByTestId('enroll-create').click();
  await bobPage.getByTestId('second-done').click();
  await expect(bobPage).toHaveURL(`${origin}/`);
  const bobCookies = await bobCtx.cookies();
  const bobCreds = await bobAuth.credentials();
  await bobCtx.close();
  await accounts(w, ['dave']);
  await w.seed('p1-w03');

  // alice opens the upgrade on bob's page.
  await page.goto(`${origin}/friends`);
  await card(page, 'bob').locator('a').first().click();
  await expect(page.getByTestId('person-relation')).toHaveAttribute('data-relation', 'friend');
  const opened = page.waitForResponse((r) => new URL(r.url()).pathname.endsWith('/upgrade') && r.status() === 201);
  const frame = page.waitForResponse((r) => /\/api\/invites\/handshakes\/[^/]+\/frame$/.test(r.url()) && r.ok());
  await page.getByTestId('action-upgrade').click();
  expect(((await (await opened).json()) as { mode: string }).mode).toBe('qr_audio');
  const { payload } = (await (await frame).json()) as { payload: string };
  expect(payload).toMatch(/^pcc2\./);
  await expect(page.getByTestId('invite-handshake')).toHaveAttribute('data-kind', 'upgrade');

  const cam = await launchWithCamera(payload);
  try {
    const ctx = await cam.browser.newContext({ baseURL: origin, ...(locale ? { locale } : {}) });
    await ctx.addCookies(bobCookies);
    const friend = await ctx.newPage();
    const auth = await authenticator(ctx, friend);
    for (const c of bobCreds) await auth.add(c);
    await installPlayer(friend);
    await friend.goto(`${origin}/join`);
    await friend.getByTestId('join-scan-friend').click();
    const sas = friend.getByTestId('join-sas');
    await expect(sas).toHaveText(/^[0-9]{6}$/, { timeout: 20_000 });
    await expect(page.getByTestId('invite-sas')).toHaveText((await sas.textContent()) ?? '');
    await page.getByTestId('invite-match').click();

    await expect(page.getByTestId('invite-listening')).toBeVisible();
    await friend.getByTestId('join-play').click();
    await relaySound(friend, page, { ultra: true, jingle: true });
    await expect(page.getByTestId('invite-outcome')).toHaveAttribute('data-outcome', 'verified');

    // Both friends sign the friend+ Friendship; the dialogs open by themselves.
    await sign(page, 'friendship');
    await sign(friend, 'friendship');
    await expect(friend.getByTestId('join-friend-plus')).toBeVisible({ timeout: 20_000 });
  } finally {
    await cam.close();
  }
  await expect(page.getByTestId('invite-done')).toBeVisible();
  await expect(page.getByTestId('person-relation')).toHaveAttribute('data-relation', 'friend_plus');
  expect(await friends(page)).toContainEqual(expect.objectContaining({ handle: 'bob', relation: 'friend_plus' }));
  await expect(page.getByTestId('action-upgrade')).toHaveCount(0);
});
