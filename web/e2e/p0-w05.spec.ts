// P0-W05 end to end: the composer (code-point counter, audience, publish), the
// post management page (edit, slippery with the detach-all offer, delete), a
// reply to an audience-limited post with the composer's warning, split into a
// thread, and the server's uniform answers. Selectors avoid English text so the
// spec runs in en and in the pseudo-locale en-XA.
import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { World } from './fixtures/world';

interface PostJSON {
  ref: string;
  body: string;
  audience: string;
  mine: boolean;
  context?: { ref: string; author_handle: string }[];
  thread?: { index: number; total: number };
}

interface PersonJSON {
  ref: string;
  handle: string;
}

const sameOrigin = { 'Sec-Fetch-Site': 'same-origin' };

function headers(origin: string, csrf: string): Record<string, string> {
  return { Origin: origin, ...sameOrigin, 'X-PCC-CSRF': csrf };
}

// setup resets the run and loads the fixture: alice – bob – carol with two
// posts by bob (one open, one friends-only), xavier in another component.
async function setup(request: APIRequestContext, baseURL: string | undefined): Promise<World> {
  const world = new World(request, baseURL ?? '');
  await world.reset();
  await world.seed('p0-w05');
  return world;
}

async function loginAs(page: Page, baseURL: string | undefined, handle: string): Promise<string> {
  await page.context().clearCookies();
  return new World(page.request, baseURL ?? '').login(handle);
}

async function getJSON<T>(req: APIRequestContext, path: string): Promise<T> {
  const res = await req.get(path, { headers: sameOrigin });
  expect(res.status(), path).toBe(200);
  return (await res.json()) as T;
}

// postsOf lists the visible recent posts of the friend with handle, as viewer sees them.
async function postsOf(req: APIRequestContext, handle: string): Promise<PostJSON[]> {
  const friends = await getJSON<{ items: PersonJSON[] }>(req, '/api/friends');
  const p = friends.items.find((f) => f.handle === handle);
  expect(p, handle).toBeDefined();
  return (await getJSON<{ items: PostJSON[] }>(req, `/api/people/${p?.ref ?? ''}/posts`)).items;
}

test('compose with the counter, then edit, turn slippery, detach all and delete', async ({ page, request, baseURL, playwright }) => {
  const origin = baseURL ?? '';
  await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');

  await page.goto('/compose');
  const body = page.getByTestId('composer-body');
  const counter = page.getByTestId('composer-counter');
  await body.fill('😀'.repeat(279));
  await expect(counter).toHaveCount(0);
  await body.fill('😀'.repeat(280)); // 560 UTF-16 units, 280 code points
  await expect(counter).toHaveAttribute('data-state', 'normal');
  await body.fill('x'.repeat(501));
  await expect(counter).toHaveAttribute('data-state', 'amber');
  await expect(page.getByTestId('composer-split')).toBeVisible();
  await body.fill('😀'.repeat(20001));
  await expect(counter).toHaveAttribute('data-state', 'over');
  await expect(page.getByTestId('composer-publish')).toBeDisabled();

  await body.fill('Hello from the composer');
  await page.getByTestId('post-audience').selectOption('d2');
  await page.getByTestId('composer-publish').click();
  const published = page.getByTestId('composer-published');
  await expect(published).toBeVisible();
  await expect(published.getByTestId('integrity-audience')).toHaveAttribute('data-audience', 'd2');

  // bob replies to it (through the API, from his own session).
  const bob = await playwright.request.newContext({ baseURL: origin });
  const bobCSRF = await new World(bob, origin).login('bob');
  const mine = (await postsOf(bob, 'alice')).find((p) => p.body === 'Hello from the composer');
  expect(mine).toBeDefined();
  const reply = await bob.post('/api/posts', {
    headers: headers(origin, bobCSRF),
    data: { body: 'A reply from bob', lang: 'en', parents: [{ ref: mine?.ref ?? '', kind: 'ref' }] },
  });
  expect(reply.status()).toBe(201);
  const replyRef = ((await reply.json()) as PostJSON).ref;
  expect(((await reply.json()) as PostJSON).context?.[0]?.author_handle).toBe('alice');

  // alice manages her post: a new version, then replies off with the offer.
  await page.getByTestId('composer-manage').click();
  await expect(page.getByTestId('post-preview')).toBeVisible();
  await expect(page.getByTestId('integrity-edited')).toBeHidden();
  await page.getByTestId('post-edit-body').fill('Hello again, edited');
  await page.getByTestId('post-edit-save').click();
  await expect(page.getByTestId('post-edit-saved')).toBeVisible();
  await expect(page.getByTestId('integrity-edited')).toBeVisible();

  await page.getByTestId('post-policy').selectOption('nobody');
  await page.getByTestId('post-policy-save').click();
  await expect(page.getByTestId('post-detach-offer')).toBeVisible();
  await page.getByTestId('post-detach-all').click();
  await expect(page.getByTestId('post-detached')).toBeVisible();

  // bob's reply stays, without the link; replies are now off for him.
  const after = await getJSON<PostJSON>(bob, `/api/posts/${replyRef}`);
  expect(after.context ?? []).toEqual([]);
  const refused = await bob.post('/api/posts', {
    headers: headers(origin, bobCSRF),
    data: { body: 'Another reply', lang: 'en', parents: [{ ref: mine?.ref ?? '', kind: 'ref' }] },
  });
  expect(refused.status()).toBe(422);
  expect(((await refused.json()) as { error: string }).error).toBe('ref_not_allowed');
  const edited = await getJSON<PostJSON>(bob, `/api/posts/${mine?.ref ?? ''}`);
  expect(edited.body).toBe('Hello again, edited');

  await page.getByTestId('post-delete').click();
  await page.getByTestId('post-delete-confirm').click();
  await expect(page.getByTestId('post-deleted')).toBeVisible();
  const gone = await bob.get(`/api/posts/${mine?.ref ?? ''}`, { headers: sameOrigin });
  expect(gone.status()).toBe(404);
  expect(await gone.text()).toBe('{"error":"not_found"}\n');
  await bob.dispose();
});

test('reply to a friends-only post with the warning, then split a long post into a thread', async ({ page, request, baseURL }) => {
  await setup(request, baseURL);
  await loginAs(page, baseURL, 'alice');
  const limited = (await postsOf(page.request, 'bob')).find((p) => p.audience === 'd1');
  expect(limited).toBeDefined();

  await page.goto(`/compose?parent=${limited?.ref ?? ''}&kind=ref`);
  await expect(page.getByTestId('composer-parent')).toBeVisible();
  await expect(page.getByTestId('composer-audience-warning')).toBeVisible();
  await page.getByTestId('composer-body').fill('Replying to a friends-only post');
  await page.getByTestId('composer-publish').click();
  const published = page.getByTestId('composer-published');
  await expect(published).toBeVisible();
  await expect(published.getByTestId('post-context')).toHaveCount(1);

  // An open post carries no warning.
  const open = (await postsOf(page.request, 'bob')).find((p) => p.audience === 'connected' && !p.context?.length);
  await page.goto(`/compose?parent=${open?.ref ?? ''}&kind=ref`);
  await expect(page.getByTestId('composer-parent')).toBeVisible();
  await expect(page.getByTestId('composer-audience-warning')).toHaveCount(0);

  // Split: two long paragraphs become two editable parts, published as one thread.
  await page.goto('/compose');
  const para = (w: string) => Array.from({ length: 60 }, () => w).join(' ') + '.';
  await page.getByTestId('composer-body').fill(`${para('first')}\n\n${para('second')}`);
  await page.getByTestId('composer-split').click();
  const parts = page.getByTestId('composer-part');
  await expect(parts).toHaveCount(2);
  await parts.nth(1).fill('The second part, shortened.');
  await page.getByTestId('composer-publish').click();
  await expect(page.getByTestId('composer-published')).toBeVisible();
  await expect(page.getByTestId('composer-published').getByTestId('post-preview')).toHaveCount(2);
  await expect(page.getByTestId('composer-published').getByTestId('post-thread').nth(1)).toBeVisible();

  // Continue the thread from its last part.
  await page.getByTestId('composer-manage').click();
  await page.getByTestId('post-continue').click();
  await expect(page.getByTestId('composer-parent')).toBeVisible();
  await page.getByTestId('composer-body').fill('A third part, added later.');
  await page.getByTestId('composer-publish').click();
  await expect(page.getByTestId('composer-published').getByTestId('post-thread')).toBeVisible();
});

test('the server counts code points and answers invisible references uniformly', async ({ page, request, baseURL }) => {
  const origin = baseURL ?? '';
  await setup(request, baseURL);
  const csrf = await loginAs(page, baseURL, 'alice');
  const post = (data: unknown) => page.request.post('/api/posts', { headers: headers(origin, csrf), data });

  expect((await post({ body: '😀'.repeat(20000), lang: 'und' })).status()).toBe(201);
  const long = await post({ body: '😀'.repeat(20001), lang: 'und' });
  expect(long.status()).toBe(422);
  expect(((await long.json()) as { error: string }).error).toBe('body_too_long');

  // xavier's post is in another component: alice holds no ref that opens it, and
  // a garbage ref gets the very same answer.
  const bobPosts = await postsOf(page.request, 'bob');
  const answers = new Set<string>();
  for (const ref of ['AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA', `${bobPosts[0]?.ref ?? ''}x`]) {
    const res = await post({ body: '', lang: 'en', parents: [{ ref, kind: 'ref' }] });
    answers.add(`${String(res.status())} ${await res.text()}`);
  }
  expect([...answers]).toEqual(['404 {"error":"not_found"}\n']);
  await page.goto('/p/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA/edit');
  await expect(page.getByTestId('post-edit-body')).toHaveCount(0);
});
