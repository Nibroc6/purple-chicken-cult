// P0-W03 end to end: suggestion → friend request → accept → friends list →
// block (with confirmation) → blocks list → unblock, through the real binary;
// plus the uniform answers a requester gets. Selectors avoid English text so
// the spec runs in en and in the pseudo-locale en-XA.
import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { World } from './fixtures/world';

// setup resets the run and loads the fixture: alice – carol – bob and
// alice – erin – bob (bob is a suggestion for alice), xavier alone.
async function setup(request: APIRequestContext, baseURL: string | undefined): Promise<World> {
  const world = new World(request, baseURL ?? '');
  await world.reset();
  await world.seed('p0-w03');
  return world;
}

async function loginAs(page: Page, baseURL: string | undefined, handle: string): Promise<string> {
  await page.context().clearCookies();
  return new World(page.request, baseURL ?? '').login(handle);
}

function headers(origin: string, csrf: string): Record<string, string> {
  return { Origin: origin, 'Sec-Fetch-Site': 'same-origin', 'X-PCC-CSRF': csrf };
}

async function getJSON<T>(page: Page, path: string): Promise<T> {
  const res = await page.request.get(path, { headers: { 'Sec-Fetch-Site': 'same-origin' } });
  expect(res.status()).toBe(200);
  return (await res.json()) as T;
}

interface PersonJSON {
  ref: string;
  handle: string;
  relation: string;
  request?: string;
}

test('suggest, request, accept, block and unblock', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');

  // bob shows up as a suggestion (two mutual friends) and alice asks.
  await page.goto('/suggestions');
  const card = page.getByTestId('person-card').filter({ hasText: '@bob' });
  await expect(card).toBeVisible();
  await expect(card.getByTestId('integrity-chain')).toBeVisible();
  await expect(card.getByTestId('integrity-inviter-label')).toBeVisible();
  await card.getByTestId('action-request').click();
  await expect(card.getByTestId('action-withdraw')).toBeVisible();

  // bob accepts from the requests inbox.
  await loginAs(page, baseURL, 'bob');
  await page.goto('/friends/requests');
  const incoming = page.getByTestId('person-card').filter({ hasText: '@alice' });
  await incoming.getByTestId('action-accept').click();
  await expect(page.getByTestId('person-card').filter({ hasText: '@alice' })).toHaveCount(0);

  await page.goto('/friends');
  const friend = page.getByTestId('person-card').filter({ hasText: '@alice' });
  await expect(friend).toBeVisible();
  await expect(friend.getByTestId('action-unfriend')).toBeVisible();

  // bob opens alice's page and blocks her (asks first).
  await friend.locator('a').first().click();
  await expect(page.getByTestId('person-relation')).toBeVisible();
  await page.getByTestId('action-block').click();
  await page.getByTestId('confirm-block').click();
  await expect(page.getByTestId('action-unblock')).toBeVisible();

  await page.goto('/settings/blocks');
  const blocked = page.getByTestId('person-card').filter({ hasText: '@alice' });
  await expect(blocked).toBeVisible();
  await blocked.getByTestId('action-unblock').click();
  await expect(page.getByTestId('person-card')).toHaveCount(0);

  // The block unfriended them; unblocking does not restore the friendship.
  const friends = await getJSON<{ items: PersonJSON[] }>(page, '/api/friends');
  expect(friends.items.map((p) => p.handle).sort()).toEqual(['carol', 'erin']);
});

test('a requester cannot tell a block or a decline from a pending request', async ({ page, request, baseURL }) => {
  const answers: string[] = [];
  for (const mode of ['pending', 'declined', 'blocked']) {
    await setup(request, baseURL);
    if (mode === 'blocked') {
      const csrf = await loginAs(page, baseURL, 'bob');
      const s = await getJSON<{ items: { person: PersonJSON }[] }>(page, '/api/suggestions');
      const alice = s.items.find((i) => i.person.handle === 'alice');
      expect(alice).toBeDefined();
      const res = await page.request.put(`/api/people/${alice?.person.ref ?? ''}/block`, { headers: headers(baseURL ?? '', csrf) });
      expect(res.status()).toBe(204);
    }
    let csrf = await loginAs(page, baseURL, 'alice');
    const s = await getJSON<{ items: { person: PersonJSON }[] }>(page, '/api/suggestions');
    const bob = s.items.find((i) => i.person.handle === 'bob');
    expect(bob).toBeDefined();
    const sent = await page.request.post(`/api/people/${bob?.person.ref ?? ''}/request`, { headers: headers(baseURL ?? '', csrf) });
    expect(sent.status()).toBe(202);
    const sentBody = await sent.text();
    if (mode === 'declined') {
      csrf = await loginAs(page, baseURL, 'bob');
      const rq = await getJSON<{ incoming: PersonJSON[] }>(page, '/api/friends/requests');
      const res = await page.request.post(`/api/friends/requests/${rq.incoming[0]?.ref ?? ''}/decline`, {
        headers: headers(baseURL ?? '', csrf),
      });
      expect(res.status()).toBe(204);
      await loginAs(page, baseURL, 'alice');
    }
    const rq = await getJSON<{ outgoing: PersonJSON[] }>(page, '/api/friends/requests');
    const person = await getJSON<PersonJSON>(page, `/api/people/${bob?.person.ref ?? ''}`);
    answers.push(JSON.stringify({ sentBody, outgoing: rq.outgoing.map((p) => [p.handle, p.request]), request: person.request }));

    await page.goto(`/people/${bob?.person.ref ?? ''}`);
    await expect(page.getByTestId('action-withdraw')).toBeVisible();
  }
  expect(answers[1]).toBe(answers[0]);
  expect(answers[2]).toBe(answers[0]);
});

test('someone in another component is not found', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  const res = await page.request.get('/api/people/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA', {
    headers: { 'Sec-Fetch-Site': 'same-origin' },
  });
  expect(res.status()).toBe(404);
  expect(await res.text()).toBe('{"error":"not_found"}\n');
  await page.goto('/people/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA');
  await expect(page.getByTestId('person-card')).toHaveCount(0);
});
