// INT-0 journey: a post travels by shortest distance, and the graph's edges
// decide everything (plan §7 required journeys; ARCHITECTURE §2.2, §4, §4.6).
// Fixture int-0: alice – bob – carol, alice – dave, bob – fran (22 recent
// posts), and xavier – yara in another component.
//  1. alice publishes through the composer; bob sees it in tier 1, carol in
//     tier 2 with the chain through @bob, xavier sees nothing and gets the
//     same 404 for it as for garbage.
//  2. bob unfriends alice from her page while his feed session is open: the
//     next page of that same session leaves her out, and carol (who reached
//     alice only through bob) loses her post on the next request.
//  3. alice blocks carol: carol's view of alice does not change, carol can
//     still reply, and gets a shadow link that alice never sees (no reply in
//     her tree, no count, no notification) and bob does not see either.
import type { Page, Response } from "@playwright/test";
import {
  at,
  actor,
  expect,
  notFound,
  garbageRef,
  seedWorld,
  signInPage,
  test,
  type FeedJSON,
  type PostJSON,
} from "./helpers";

const isFeed = (r: Response) => new URL(r.url()).pathname === "/api/feed";
const cardOf = (page: Page, body: string) =>
  page.locator('[data-testid="feed-post"]', { hasText: body });

test("tier 1 for a friend, tier 2 with the chain for a friend of a friend, nothing for another component", async ({
  page,
  request,
  baseURL,
  playwright,
}) => {
  const origin = baseURL ?? "";
  await seedWorld(request, origin, "int-0");
  await signInPage(page, origin, "alice");
  await page.goto("/compose");
  await page.getByTestId("composer-body").fill("Alice shares her garden plan");
  await page.getByTestId("composer-publish").click();
  await expect(page.getByTestId("composer-published")).toBeVisible();

  // bob: tier 1, chain "you → @alice", the genesis inviter label.
  await signInPage(page, origin, "bob");
  await page.goto("/");
  const forBob = cardOf(page, "Alice shares her garden plan");
  await expect(forBob).toHaveAttribute("data-tier", "1");
  await expect(forBob.getByTestId("integrity-chain")).toContainText(
    at("alice"),
  );
  await expect(forBob.getByTestId("integrity-chain")).not.toContainText(
    at("bob"),
  );
  await expect(forBob.getByTestId("integrity-inviter-label")).toHaveAttribute(
    "data-kind",
    "genesis",
  );

  // carol: tier 2 under its own header, the chain names bob as the first hop.
  const carol = await signInPage(page, origin, "carol");
  await page.goto("/");
  const forCarol = cardOf(page, "Alice shares her garden plan");
  await expect(forCarol).toHaveAttribute("data-tier", "2");
  await expect(forCarol.getByTestId("integrity-chain")).toContainText(
    at("bob"),
  );
  await expect(forCarol.getByTestId("integrity-chain")).toContainText(
    at("alice"),
  );
  const tier2 = page.locator('[data-testid="feed-tier"][data-tier="2"]');
  await expect(
    tier2.getByTestId("integrity-tier-header").first(),
  ).toBeVisible();
  await expect(tier2).toContainText("Alice shares her garden plan");
  const post = await carol.feedPost("Alice shares her garden plan");
  expect(post.tier).toBe(2);
  expect(post.author.chain?.first_hop?.handle).toBe("bob");

  // xavier: another component — nothing in the feed, and alice's post (by
  // carol's ref or by a guess) answers exactly like garbage.
  const xavier = await signInPage(page, origin, "xavier");
  await page.goto("/");
  await expect(cardOf(page, "Yara lives in another world")).toBeVisible();
  await expect(page.getByTestId("feed-post")).toHaveCount(1);
  await expect(page.getByText("Alice shares her garden plan")).toHaveCount(0);
  const all = await xavier.feedPosts();
  expect(all.map((p) => p.author.handle)).toEqual(["yara"]);
  for (const ref of [post.ref, garbageRef]) {
    expect(await xavier.raw(`/posts/${ref}`)).toEqual({
      status: 404,
      body: notFound,
    });
    expect(await xavier.raw(`/tree/${ref}`)).toEqual({
      status: 404,
      body: notFound,
    });
  }
  expect(await xavier.raw(`/people/${post.author.ref}`)).toEqual({
    status: 404,
    body: notFound,
  });
  const outsiders = await actor(playwright, origin, "yara");
  expect(JSON.stringify(await outsiders.get("/suggestions"))).not.toMatch(
    /alice|bob|carol|dave|fran/,
  );
});

test("unfriending takes effect on the next request, even inside an open feed session", async ({
  page,
  context,
  request,
  baseURL,
  playwright,
}) => {
  const origin = baseURL ?? "";
  await seedWorld(request, origin, "int-0");
  const carol = await actor(playwright, origin, "carol");
  const aliceForCarol = await carol.feedPost(
    "Alice asks: who wants to plant a garden?",
  );

  // bob's feed (Latest): fran's 20 newest posts on page 1, alice's on page 2.
  const bob = await signInPage(page, origin, "bob");
  const first = page.waitForResponse(isFeed);
  await page.goto("/");
  const p1 = (await (await first).json()) as FeedJSON;
  expect(p1.next).toBeTruthy();
  expect(JSON.stringify(p1)).not.toContain("Alice asks");

  // In another tab, bob unfriends alice from her page.
  const alice = await bob.friend("alice");
  const tab = await context.newPage();
  await tab.goto(`/people/${alice.ref}`);
  await expect(tab.getByTestId("person-relation")).toBeVisible();
  await tab.getByTestId("action-unfriend").click();
  // The confirm step hides action-unfriend at once, so wait for the server's
  // answer itself: closing the tab earlier can let the next page win the race.
  const unfriended = tab.waitForResponse(
    (r) =>
      r.request().method() === "DELETE" &&
      new URL(r.url()).pathname.startsWith("/api/friends/"),
  );
  await tab.getByTestId("confirm-unfriend").click();
  expect((await unfriended).ok()).toBe(true);
  await expect(tab.getByTestId("action-unfriend")).toHaveCount(0);
  await tab.close();

  // The next page of the same session: no alice.
  const next = page.waitForResponse(
    (r) => isFeed(r) && new URL(r.url()).searchParams.has("cursor"),
  );
  await page.getByTestId("feed-more").click();
  const p2 = (await (await next).json()) as FeedJSON;
  expect(p2.session).toBe(p1.session);
  const authors = p2.items
    .flatMap((it) => (it.post ? [it.post] : (it.thread ?? [])))
    .map((p: PostJSON) => p.author.handle);
  expect(authors).not.toContain("alice");
  expect(authors).toContain("carol");
  await expect(
    page.getByText("Alice asks: who wants to plant a garden?"),
  ).toHaveCount(0);
  expect(
    (await bob.get<{ items: { handle: string }[] }>("/friends")).items
      .map((f) => f.handle)
      .sort(),
  ).toEqual(["carol", "fran"]);

  // carol reached alice only through bob: her old ref is gone now.
  expect(await carol.raw(`/posts/${aliceForCarol.ref}`)).toEqual({
    status: 404,
    body: notFound,
  });
  expect((await carol.feedPosts()).map((p) => p.author.handle)).not.toContain(
    "alice",
  );
});

test("a block hides the blocked person from the blocker only; their reply is a shadow link", async ({
  page,
  request,
  baseURL,
  playwright,
}) => {
  const origin = baseURL ?? "";
  await seedWorld(request, origin, "int-0");
  const carol = await actor(playwright, origin, "carol");
  const before = await carol.feedPost(
    "Alice asks: who wants to plant a garden?",
  );

  // alice blocks carol from carol's page (reached from carol's post in her feed).
  const alice = await signInPage(page, origin, "alice");
  const carolPost = await alice.feedPost("Carol knows about compost");
  await page.goto(`/people/${carolPost.author.ref}`);
  await page.getByTestId("action-block").click();
  await page.getByTestId("confirm-block").click();
  await expect(page.getByTestId("action-unblock")).toBeVisible();
  expect((await alice.feedPosts()).map((p) => p.author.handle)).not.toContain(
    "carol",
  );

  // carol's view of alice is unchanged, and she can still reply.
  const after = await carol.feedPost(
    "Alice asks: who wants to plant a garden?",
  );
  expect(after).toMatchObject({
    ref: before.ref,
    tier: before.tier,
    reply_count: before.reply_count,
  });
  const reply = await carol.send("POST", "/posts", {
    body: "Carol replies anyway",
    lang: "en",
    parents: [{ ref: before.ref, kind: "ref" }],
  });
  expect(reply.status).toBe(201);
  const replyRef = (JSON.parse(reply.body) as PostJSON).ref;
  const own = await carol.get<PostJSON>(`/posts/${replyRef}`);
  expect(own.context?.map((c) => c.author_handle)).toEqual(["alice"]);

  // alice: no reply in her tree, no count, no notification.
  const me = await alice.get<{ ref: string }>("/me");
  const mine = (
    await alice.get<{ items: PostJSON[] }>(`/people/${me.ref}/posts`)
  ).items.find((p) => p.body === "Alice asks: who wants to plant a garden?");
  expect(mine?.reply_count).toBe(0);
  await page.goto(`/p/${mine?.ref ?? ""}`);
  await expect(page.getByTestId("tree-layout")).toBeVisible();
  await expect(page.getByText("Carol replies anyway")).toHaveCount(0);
  expect(
    (await alice.get<{ items: unknown[] }>("/notifications")).items,
  ).toEqual([]);

  // bob sees carol's reply without the hidden link.
  const bob = await actor(playwright, origin, "bob");
  const forBob = await bob.feedPost("Carol replies anyway");
  expect(forBob.context ?? []).toEqual([]);
  const aliceForBob = await bob.feedPost(
    "Alice asks: who wants to plant a garden?",
  );
  expect(aliceForBob.reply_count).toBe(0);
});
