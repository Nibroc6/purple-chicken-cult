// INT-0 journeys across posts, engagement, notifications, moderation, recovery
// and account deletion (plan §7 required journeys). Fixture int-0: alice – bob –
// carol, alice – dave, bob – fran; mona and mike are moderators; carol was
// invited by bob.
import type { Response } from '@playwright/test';
import { at, actor, expect, notFound, seedWorld, signInPage, test, type FeedJSON, type PostJSON } from './helpers';

const isFeed = (r: Response) => new URL(r.url()).pathname === '/api/feed';

test('thread, a reply to two parents, the tree, likes in the network, seen and presets', async ({ page, request, baseURL, playwright }) => {
  test.setTimeout(90_000);
  const origin = baseURL ?? '';
  await seedWorld(request, origin, 'int-0');

  // bob splits a long text into a two-part thread in the composer.
  await signInPage(page, origin, 'bob');
  await page.goto('/compose');
  const para = (w: string) => Array.from({ length: 60 }, () => w).join(' ') + '.';
  await page.getByTestId('composer-body').fill(`${para('seeds')}\n\n${para('soil')}`);
  await page.getByTestId('composer-split').click();
  await expect(page.getByTestId('composer-part')).toHaveCount(2);
  await page.getByTestId('composer-publish').click();
  await expect(page.getByTestId('composer-published').getByTestId('post-preview')).toHaveCount(2);

  // alice gets it as one thread card in tier 1.
  const alice = await signInPage(page, origin, 'alice');
  await page.goto('/');
  const thread = page.getByTestId('feed-thread').filter({ hasText: 'seeds seeds' });
  await expect(thread).toHaveAttribute('data-parts', '2');
  await expect(thread.locator('[data-seen-ref]')).toHaveCount(1);
  const parts = (await alice.feedPosts()).filter((p) => p.thread);
  expect(parts.map((p) => p.thread)).toEqual([
    { index: 1, total: 2 },
    { index: 2, total: 2 },
  ]);

  // carol (two steps from alice) replies to alice's post and to bob's thread at once.
  const carol = await actor(playwright, origin, 'carol');
  const aliceForCarol = await carol.feedPost('Alice asks: who wants to plant a garden?');
  const threadForCarol = (await carol.feedPosts()).find((p) => p.thread?.index === 2);
  expect(aliceForCarol.tier).toBe(2);
  const created = await carol.send('POST', '/posts', {
    body: 'Carol answers both of you',
    lang: 'en',
    parents: [
      { ref: aliceForCarol.ref, kind: 'ref' },
      { ref: threadForCarol?.ref ?? '', kind: 'ref' },
    ],
  });
  expect(created.status).toBe(201);
  expect((JSON.parse(created.body) as PostJSON).context?.map((c) => c.author_handle).sort()).toEqual(['alice', 'bob']);

  // alice's notification opens the tree of her post with carol's reply below
  // ("also replies to @bob"); refocusing on the reply shows both parents above.
  await page.goto('/notifications');
  const note = page.locator('[data-testid="notification"][data-kind="post_referenced"]');
  await expect(note).toHaveCount(1);
  await note.getByTestId('notification-post').click();
  await expect(page.locator('[data-testid="tree-row"][data-gen="0"]')).toContainText('Alice asks: who wants to plant a garden?');
  const item = page.locator('[data-testid="tree-row"][data-gen="1"]').getByTestId('tree-item').filter({ hasText: 'Carol answers both of you' });
  await expect(item.getByTestId('tree-also')).toContainText(at('bob'));
  await item.getByTestId('feed-tree').click();
  const parents = page.locator('[data-testid="tree-row"][data-gen="-1"]');
  await expect(parents.getByTestId('tree-item')).toHaveCount(2);
  await expect(parents).toContainText('Alice asks: who wants to plant a garden?');
  await expect(parents).toContainText('soil soil');
  const focal = page.locator('[data-testid="tree-row"][data-gen="0"]');
  await expect(focal).toContainText('Carol answers both of you');
  await expect(focal.getByTestId('integrity-chain')).toContainText(at('bob'));

  // alice likes it; for bob (one step from alice) the like counts and names her,
  // for fran (two steps from alice) it counts without a name she can't see.
  const reply = await alice.feedPost('Carol answers both of you');
  await page.goto(`/p/${reply.ref}/likes`);
  await page.getByTestId('engage-like').click();
  await expect(page.getByTestId('engage-likes')).toHaveAttribute('data-count', '1');
  const bob = await actor(playwright, origin, 'bob');
  const forBob = await bob.feedPost('Carol answers both of you');
  expect(forBob.likes.in_network).toBe(1);
  expect(forBob.likes.names?.map((n) => n.handle)).toEqual(['alice']);
  const fran = await actor(playwright, origin, 'fran');
  const forFran = await fran.feedPost('Carol answers both of you');
  expect(forFran.likes).toMatchObject({ in_network: 1, others: 1 });
  expect(forFran.likes.names ?? []).toEqual([]);

  // Seen: cards alice looked at land in recently seen and leave her next session.
  await page.goto('/');
  await expect(page.locator('[data-seen-ref]').first()).toBeVisible();
  await expect
    .poll(async () => (await alice.get<{ items: PostJSON[] }>('/seen/recent')).items.length, { timeout: 15_000, intervals: [500] })
    .toBeGreaterThan(0);
  const seen = (await alice.get<{ items: PostJSON[] }>('/seen/recent')).items.map((p) => p.ref);
  const fresh = (await alice.feedPosts()).map((p) => p.ref);
  for (const ref of seen) expect(fresh).not.toContain(ref);
  await page.goto('/seen');
  await expect(page.getByTestId('seen-item').first()).toBeVisible();

  // Presets: Balanced and back to Latest, each saved as the default.
  await page.goto('/');
  for (const preset of ['balanced', 'latest'] as const) {
    const res = page.waitForResponse((r) => isFeed(r) && new URL(r.url()).searchParams.get('preset') === preset);
    await page.getByTestId(`feed-preset-${preset}`).click();
    expect(((await (await res).json()) as FeedJSON).preset).toBe(preset);
    await expect(page.getByTestId(`feed-preset-${preset}`)).toHaveAttribute('aria-pressed', 'true');
    await expect.poll(async () => (await alice.get<{ settings: { rank_preset: string } }>('/me')).settings.rank_preset).toBe(preset);
  }
});

test('report, moderator removal, the inviter blamed; a held recovery cancelled; an account deleted', async ({
  page,
  request,
  baseURL,
  playwright,
  newWatchedContext,
}) => {
  test.setTimeout(90_000);
  const origin = baseURL ?? '';
  const world = await seedWorld(request, origin, 'int-0');

  // alice reports carol's post from the card's report action.
  const alice = await signInPage(page, origin, 'alice');
  const compost = await alice.feedPost('Carol knows about compost');
  await page.goto(`/p/${compost.ref}`);
  const card = page.locator('[data-testid="tree-row"][data-gen="0"]');
  await card.getByTestId('integrity-report').first().click();
  // The report opens as a modal over the reply tree.
  const reportModal = page.getByTestId('report-modal');
  await expect(reportModal).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`/p/${compost.ref}\\?report=post%3A${compost.ref}$`));
  await reportModal.locator('input[type=radio][value=spam]').check();
  await reportModal.getByTestId('report-submit').click();
  await expect(reportModal.getByTestId('report-sent')).toBeVisible();

  // mona: the queue item names carol and her inviter, and gives her account reference.
  await signInPage(page, origin, 'mona');
  await page.goto('/mod');
  const item = page.locator('[data-testid="queue-item"][data-reason="spam"]');
  await expect(item.getByTestId('queue-post')).toHaveText('Carol knows about compost');
  await expect(item.getByTestId('queue-subject')).toContainText(at('carol'));
  await expect(item.getByTestId('queue-inviter')).toContainText(at('bob'));
  const subject = (await item.getByTestId('queue-subject-ref').textContent()) ?? '';
  expect(subject).not.toBe('');
  await item.getByTestId('queue-action').selectOption('remove_post');
  await item.getByTestId('queue-reason').selectOption('spam');
  await item.getByTestId('queue-resolve').click();
  await expect(page.getByTestId('queue-empty')).toBeVisible();

  // The post is gone for everyone; bob, who invited carol, has it in his blame inbox.
  expect(await alice.raw(`/posts/${compost.ref}`)).toEqual({ status: 404, body: notFound });
  const bob = await actor(playwright, origin, 'bob');
  const blame = await bob.get<{ items: { action: string; invitee_handle: string }[] }>('/blame');
  expect(blame.items).toEqual([expect.objectContaining({ action: 'remove_post', invitee_handle: 'carol' })]);
  expect(JSON.stringify(blame)).not.toContain('alice');

  // Manual recovery of carol's account: mona opens it, mike approves.
  await page.goto('/mod/recovery');
  const forms = page.locator('form.settings-form');
  await forms.nth(0).locator('input').fill(subject);
  await forms.nth(0).locator('button[type=submit]').click();
  const id = (await forms.nth(0).locator('[role=status] code').textContent()) ?? '';
  expect(id).not.toBe('');
  await signInPage(page, origin, 'mike');
  await page.goto('/mod/recovery');
  await forms.nth(1).locator('input').fill(id);
  await forms.nth(1).locator('button[type=submit]').click();
  await expect(forms.nth(1).locator('[role=status]')).toBeVisible();
  // The link waits for the 24 h hold.
  await forms.nth(2).locator('input').fill(id);
  await forms.nth(2).locator('button[type=submit]').click();
  await expect(forms.nth(2).locator('[role=alert]')).toBeVisible();

  // carol's live session shows the warning and cancels; bob was told.
  const { page: carolPage } = await newWatchedContext();
  await signInPage(carolPage, origin, 'carol');
  await carolPage.goto('/');
  const banner = carolPage.locator('.recovery-banner[role=alert]');
  await expect(banner).toBeVisible();
  await banner.locator('button').click();
  await expect(carolPage.locator('.recovery-banner[role=status]')).toBeVisible();
  const told = await bob.get<{ items: { kind: string; data?: { reason?: string } }[] }>('/notifications');
  expect(told.items.some((n) => n.kind === 'friend_security')).toBe(true);
  // After the hold the cancelled recovery yields no link.
  await world.advance('25h');
  const mike = await actor(playwright, origin, 'mike');
  const bobLater = await actor(playwright, origin, 'bob');
  expect(await mike.raw(`/mod/recovery/${id}/link`)).toEqual({ status: 404, body: notFound });

  // carol deletes her account (fresh passkey, typed handle): her posts leave
  // every view, bob is told, the handle stays taken.
  const carol = await actor(playwright, origin, 'carol');
  expect((await carol.send('POST', '/posts', { body: 'Carol says goodbye', lang: 'en' })).status).toBe(201);
  const goodbye = await bobLater.feedPost('Carol says goodbye');
  await signInPage(carolPage, origin, 'carol');
  await carolPage.goto('/settings/delete');
  await carolPage.locator('form input').fill('carol');
  await carolPage.locator('form button[type=submit]').click();
  await expect(carolPage).toHaveURL(`${origin}/`);
  expect(await bobLater.raw(`/posts/${goodbye.ref}`)).toEqual({ status: 404, body: notFound });
  expect((await bobLater.feedPosts()).map((p) => p.author.handle)).not.toContain('carol');
  // The account.delete job revokes her edges and tells her friends.
  await expect
    .poll(async () => (await bobLater.get<{ items: { handle: string }[] }>('/friends')).items.map((f) => f.handle), { timeout: 15_000 })
    .not.toContain('carol');
  const notes = await bobLater.get<{ items: { kind: string; data?: { reason?: string } }[] }>('/notifications');
  expect(notes.items.some((n) => n.kind === 'friend_security' && n.data?.reason === 'account_deleted')).toBe(true);
  const again = await request.post('/api/_test/genesis', { data: { handle: 'carol' }, headers: { Origin: origin, 'Sec-Fetch-Site': 'same-origin' } });
  expect(again.ok()).toBe(false);
});
