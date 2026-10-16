// INT-0 journey: from nothing to a conversation, all through the UI with real
// passkeys (virtual authenticators) and a real camera pipeline (fake device),
// then a signed export verified offline (plan §7 required journeys).
//  1. A genesis account (alice) enrolls from its one-time link.
//  2. alice shows her rotating invite QR; bob's browser films it, both screens
//     show the same SAS, alice confirms, bob registers his passkey: they are
//     friends, bob is "invited by @alice" and on probation.
//  3. bob posts (probation forces friends-only, which only he is told); alice
//     finds it in tier 1 with the chain and inviter label, and replies from
//     the card.
//  4. bob's badge lights up live; the notification opens the tree with
//     alice's reply in the replies row; bob likes it; alice sees the like
//     counted and bob named.
//  5. alice downloads her export (fresh passkey, no step-up needed) and
//     `pcc export-verify` accepts every record against the published keys.
import { execFile } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';
import { request as pwRequest } from '@playwright/test';
import { addVirtualAuthenticator } from '../fixtures/auth';
import { launchWithCamera } from '../fixtures/camera';
import { World } from '../fixtures/world';
import { Actor, expect, sameOrigin, test, type PostJSON } from './helpers';

const run = promisify(execFile);

test('enroll, invite in person, post, reply, notify, like, export and verify', async ({ page, context, baseURL, newWatchedContext }) => {
  test.setTimeout(120_000);
  const origin = baseURL ?? '';
  const hooks = await pwRequest.newContext({ baseURL: origin });
  const world = new World(hooks, origin);
  await world.reset();

  // 1. alice enrolls (genesis) with a passkey.
  const token = await world.genesisToken('alice');
  await addVirtualAuthenticator(context, page);
  await page.goto(`/enroll#${token}`);
  await page.locator('input[name=tos]').check();
  await page.locator('input[name=age]').check();
  await page.getByTestId('enroll-create').click();
  await page.getByTestId('second-done').click();
  await expect(page).toHaveURL(`${origin}/`);
  await expect(page.getByTestId('feed-page')).toBeVisible();

  // 2. The in-person invite.
  await page.goto('/invite');
  await expect(page.getByTestId('invite-available')).toBeVisible();
  const frame = page.waitForResponse((r) => /\/api\/invites\/handshakes\/[^/]+\/frame$/.test(r.url()) && r.ok());
  await page.getByTestId('invite-open').click();
  const { payload } = (await (await frame).json()) as { payload: string };
  const cam = await launchWithCamera(payload);
  try {
    const { ctx, page: bob } = await newWatchedContext(cam.browser);
    await addVirtualAuthenticator(ctx, bob);
    await bob.goto('/join');
    await bob.getByTestId('join-scan').click();
    const sas = bob.getByTestId('join-sas');
    await expect(sas).toHaveText(/^[0-9]{6}$/, { timeout: 20_000 });
    await expect(page.getByTestId('invite-sas')).toHaveText((await sas.textContent()) ?? '');
    await page.getByTestId('invite-match').click();
    await bob.locator('input[name=handle]').fill('bob');
    await bob.locator('input[name=display_name]').fill('Bob');
    await bob.locator('input[name=tos]').check();
    await bob.locator('input[name=age]').check();
    await bob.getByTestId('join-submit').click();
    await expect(bob.getByTestId('join-invited-by')).toContainText('@alice');
    await bob.getByTestId('second-done').click();
    await expect(bob).toHaveURL(`${origin}/`);
    await expect(page.getByTestId('invite-done')).toBeVisible();

    // Both see the friendship; the inviter label tells who invited whom.
    await page.goto('/friends');
    const bobCard = page.getByTestId('person-card').filter({ hasText: '@bob' });
    await expect(bobCard.getByTestId('integrity-inviter-label')).toContainText('@alice');
    await bob.goto('/friends');
    const aliceCard = bob.getByTestId('person-card').filter({ hasText: '@alice' });
    await expect(aliceCard.getByTestId('integrity-inviter-label')).toHaveAttribute('data-kind', 'genesis');
    await page.goto('/invite/people');
    await expect(page.getByTestId('person-card').filter({ hasText: '@bob' }).getByTestId('invitee-probation')).toBeVisible();

    // 3. bob posts; probation forces friends-only, and only he is told.
    await bob.goto('/compose');
    await bob.getByTestId('composer-body').fill('Hello, I just joined');
    await bob.getByTestId('composer-publish').click();
    const published = bob.getByTestId('composer-published');
    await expect(published.getByTestId('integrity-audience')).toHaveAttribute('data-audience', 'd1');
    await expect(published.getByTestId('integrity-audience')).toHaveAttribute('data-forced', 'true');

    // alice's feed: tier 1, the chain to @bob, bob's inviter label; she replies from the card.
    await page.goto('/');
    const card = page.locator('[data-testid="feed-post"]', { hasText: 'Hello, I just joined' });
    await expect(card).toHaveAttribute('data-tier', '1');
    await expect(page.getByTestId('integrity-tier-header').first()).toHaveAttribute('data-tier', '1');
    await expect(card.getByTestId('integrity-chain')).toContainText('@bob');
    await expect(card.getByTestId('integrity-inviter-label')).toContainText('@alice');
    await expect(card.getByTestId('integrity-audience')).toHaveAttribute('data-audience', 'd1');
    await expect(card.getByTestId('integrity-audience')).not.toHaveAttribute('data-forced', 'true');
    await card.getByTestId('feed-reply').click();
    await expect(page.getByTestId('composer-parent')).toBeVisible();
    await page.getByTestId('composer-body').fill('Welcome, @bob!');
    await page.getByTestId('composer-publish').click();
    await expect(page.getByTestId('composer-published').getByTestId('post-context')).toHaveCount(1);

    // 4. bob's badge (live over SSE), the notification, the tree, the like.
    await bob.goto('/');
    await expect(bob.getByTestId('notifications-badge')).toBeVisible({ timeout: 10_000 });
    await bob.goto('/notifications');
    const note = bob.getByTestId('notification').first();
    await expect(note).toBeVisible();
    await note.getByTestId('notification-post').click();
    await expect(bob.getByTestId('tree-layout')).toBeVisible();
    await expect(bob.getByTestId('tree-layout')).toContainText('Welcome, @bob!');
    const bobAPI = (await (await bob.request.get('/api/session', { headers: sameOrigin })).json()) as { csrf?: string };
    const bobActor = new Actor(bob.request, origin, bobAPI.csrf ?? '');
    const welcome = (await bobActor.feedPosts()).find((p: PostJSON) => p.body === 'Welcome, @bob!');
    expect(welcome?.tier).toBe(1);
    await bob.goto(`/p/${welcome?.ref ?? ''}/likes`);
    await bob.getByTestId('engage-like').click();
    await expect(bob.getByTestId('engage-like')).toHaveAttribute('aria-pressed', 'true');

    const alice = (await (await page.request.get('/api/session', { headers: sameOrigin })).json()) as { csrf?: string };
    const aliceActor = new Actor(page.request, origin, alice.csrf ?? '');
    const me = await aliceActor.get<{ ref: string }>('/me');
    const mine = (await aliceActor.get<{ items: PostJSON[] }>(`/people/${me.ref}/posts`)).items.find((p) => p.body === 'Welcome, @bob!');
    expect(mine?.likes).toMatchObject({ in_network: 1, by_me: false });
    expect(mine?.likes.names?.map((n) => n.handle)).toEqual(['bob']);
    await page.goto(`/p/${mine?.ref ?? ''}/likes`);
    await expect(page.getByTestId('engage-likes')).toHaveAttribute('data-count', '1');
  } finally {
    await cam.close();
  }

  // 5. Export (the enrollment passkey is still fresh) and verify it offline.
  await page.goto('/settings/export');
  const download = page.waitForEvent('download');
  await page.locator('.settings-actions button').click();
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'pcc-export-'));
  try {
    const zip = path.join(dir, 'export.zip');
    await (await download).saveAs(zip);
    const keys = (await (await page.request.get('/api/keys', { headers: sameOrigin })).json()) as {
      server: { key_id: string; fingerprint: string }[];
    };
    expect(keys.server.length).toBeGreaterThan(0);
    const pins = path.join(dir, 'fingerprints.txt');
    fs.writeFileSync(pins, keys.server.map((k) => `${k.key_id}  ${k.fingerprint}`).join('\n') + '\n');
    const bin = process.env.PCC_E2E_BIN ?? path.resolve(import.meta.dirname, '..', '..', '..', 'var', 'pcc');
    const { stdout } = await run(bin, ['export-verify', zip, '--fingerprints', pins]);
    const report = JSON.parse(stdout) as { records: number; valid: number; failures?: unknown[]; keys_pinned: boolean; bodies_checked: number };
    expect(report.failures ?? []).toEqual([]);
    expect(report.keys_pinned).toBe(true);
    expect(report.valid).toBe(report.records);
    // Her enrollment, her invite of bob, the friendship and her reply at least.
    expect(report.records).toBeGreaterThanOrEqual(4);
    expect(report.bodies_checked).toBeGreaterThanOrEqual(1);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
  await hooks.dispose();
});
