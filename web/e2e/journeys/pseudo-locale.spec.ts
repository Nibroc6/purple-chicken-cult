// INT-0 pseudo-locale pass (plan §6.5, ARCHITECTURE §18 Phase 0 DoD): every
// page of the app, signed out, as a member with real data (feed, tree, likes,
// people, requests, notifications, seen, settings, invites, composer, report,
// blame) and as a moderator, renders in en-XA with no visible text outside the
// catalogs (user content lives in data-user-content containers) and no
// truncated integrity element. Runs in the en-XA project only.
import type { Locator, Page } from '@playwright/test';
import { Actor, actor, expect, feedToEnd, garbageRef, seedWorld, signInPage, test, truncatedIntegrity, untranslated } from './helpers';

test.skip(({ locale }) => locale !== 'en-XA', 'the pseudo-locale pass runs in the en-XA project');

// failures collects what every page got wrong, so one run reports them all.
const failures: string[] = [];

// settled polls probe until it finds nothing (translations and data load
// lazily) or 5 s pass, and returns the last findings.
async function settled(probe: () => Promise<string[]>): Promise<string[]> {
  const deadline = Date.now() + 5_000;
  for (;;) {
    const found = await probe();
    if (found.length === 0 || Date.now() > deadline) return found;
    await new Promise((r) => setTimeout(r, 250));
  }
}

// check opens path, waits for ready (default: the page heading), then records
// any untranslated text or truncated integrity element on it.
async function check(page: Page, path: string, ready?: (p: Page) => Locator, open = true): Promise<void> {
  if (open) await page.goto(path);
  await expect((ready ?? ((p: Page) => p.getByRole('heading', { level: 1 })))(page).first()).toBeVisible();
  for (const f of await settled(() => untranslated(page))) failures.push(`${path} untranslated ${f}`);
  for (const f of await truncatedIntegrity(page)) failures.push(`${path} truncated ${f}`);
}

test('every page renders in the pseudo-locale with no untranslated or truncated text', async ({ page, request, baseURL, playwright }) => {
  test.setTimeout(120_000);
  const origin = baseURL ?? '';
  await seedWorld(request, origin, 'int-0');

  // State for the pages: a reply and a like for alice's notifications, an
  // incoming friend request, a removed post (bob's blame inbox), a report in
  // the moderators' queue and an edited, audience-limited post.
  const bob = await actor(playwright, origin, 'bob');
  const alicePost = await bob.feedPost('Alice asks: who wants to plant a garden?');
  expect((await bob.send('POST', '/posts', { body: 'Bob replies to @alice', lang: 'en', parents: [{ ref: alicePost.ref, kind: 'ref' }] })).status).toBe(201);
  expect((await bob.send('PUT', `/posts/${alicePost.ref}/like`)).status).toBe(200);
  const fran = await actor(playwright, origin, 'fran');
  const carolForFran = (await fran.feedPosts()).find((p) => p.author.handle === 'carol');
  expect(carolForFran).toBeDefined();
  const aliceForFran = (await fran.feedPosts()).find((p) => p.author.handle === 'alice');
  expect((await fran.send('POST', `/people/${aliceForFran?.author.ref ?? ''}/request`)).status).toBe(202);
  expect((await fran.send('POST', '/reports', { target: { kind: 'post', ref: carolForFran?.ref ?? '' }, reason: 'spam' })).status).toBe(202);
  const mona = await actor(playwright, origin, 'mona');
  const item = (await mona.get<{ items: { id: string }[] }>('/mod/queue')).items[0];
  expect((await mona.send('POST', `/mod/items/${item?.id ?? ''}/resolve`, { action: 'remove_post', reason: 'spam' })).status).toBe(204);
  expect((await fran.send('POST', '/reports', { target: { kind: 'person', ref: aliceForFran?.author.ref ?? '' }, reason: 'other' })).status).toBe(202);

  // Signed out.
  await check(page, '/', (p) => p.getByTestId('landing-page'));
  await check(page, '/login');
  await check(page, '/join');
  await check(page, '/terms', (p) => p.getByTestId('terms-fingerprint'));
  await check(page, '/enroll');
  await check(page, '/no/such/page');

  // alice, a member with data everywhere.
  const alice: Actor = await signInPage(page, origin, 'alice');
  const mine = await alice.send('POST', '/posts', { body: 'Alice edits this one', lang: 'en', audience: 'd2' });
  expect(mine.status).toBe(201);
  const mineRef = (JSON.parse(mine.body) as { ref: string }).ref;
  expect((await alice.send('PATCH', `/posts/${mineRef}`, { body: 'Alice edited this one' })).status).toBe(200);
  const bobForAlice = await alice.friend('bob');
  const bobPost = await alice.feedPost('Bob has seeds to share');

  await check(page, '/', (p) => p.getByTestId('feed-post'));
  await feedToEnd(page);
  await expect(page.getByTestId('integrity-tier-header').first()).toBeVisible();
  // The "why" panel of the first card.
  await page.getByTestId('feed-post').first().getByTestId('integrity-why').click();
  await check(page, '/ (why panel)', (p) => p.getByTestId('integrity-why-body'), false);
  await check(page, `/p/${bobPost.ref}`, (p) => p.getByTestId('tree-layout'));
  await check(page, `/p/${(await alice.feedPost('Dave brings a shovel')).ref}`, (p) => p.getByTestId('tree-layout'));
  await check(page, `/p/${bobPost.ref}/likes`, (p) => p.getByTestId('engage-card'));
  await check(page, `/p/${mineRef}/edit`, (p) => p.getByTestId('post-edit-body'));
  await check(page, `/people/${bobForAlice.ref}`, (p) => p.getByTestId('person-relation'));
  await check(page, '/friends', (p) => p.getByTestId('person-card'));
  await check(page, '/friends/requests', (p) => p.getByTestId('person-card'));
  await check(page, '/suggestions');
  await check(page, '/settings/blocks');
  await check(page, '/notifications', (p) => p.getByTestId('notification'));
  await check(page, '/seen');
  await check(page, '/settings');
  for (const s of ['display', 'languages', 'privacy', 'export', 'delete', 'passkeys']) await check(page, `/settings/${s}`);
  await check(page, '/invite', (p) => p.getByTestId('invite-available'));
  await check(page, '/invite/people', (p) => p.getByTestId('person-card'));
  await check(page, '/compose', (p) => p.getByTestId('composer-body'));
  await check(page, `/compose?parent=${bobPost.ref}&kind=ref`, (p) => p.getByTestId('composer-parent'));
  await check(page, `/report?kind=post&ref=${bobPost.ref}`, (p) => p.getByTestId('report-submit'));
  await check(page, `/report?kind=inviter&ref=${garbageRef}`, (p) => p.getByTestId('report-submit'));
  await check(page, '/mod/log');
  await check(page, '/mod/recovery');
  // The Compact format's feed and tree.
  expect((await alice.send('PATCH', '/me/settings', { ui_prefs: { format: 'compact' } })).status).toBe(200);
  await check(page, '/', (p) => p.locator('[data-testid="feed-layout"][data-format="compact"] [data-testid="feed-post"]'));
  await check(page, `/p/${bobPost.ref}`, (p) => p.getByTestId('tree-layout'));

  // bob: the blame inbox with an entry.
  await signInPage(page, origin, 'bob');
  await check(page, '/blame', (p) => p.getByTestId('blame-item'));

  // mona, a moderator: the queue and the recovery page.
  await signInPage(page, origin, 'mona');
  await check(page, '/mod', (p) => p.getByTestId('queue-item'));
  await check(page, '/mod/recovery', (p) => p.locator('form').first());
  await check(page, '/mod/log', (p) => p.getByTestId('log-row'));
  expect(failures).toEqual([]);
});
