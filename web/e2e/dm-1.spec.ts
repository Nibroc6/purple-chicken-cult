// Direct messages end to end: a private conversation started from a person's
// page, the unread badge, a post shared into it (a recipient who cannot see
// the post gets only a placeholder), and a group (created, joined from the
// invitation, messaged). Selectors avoid English text so the spec runs in en
// and en-XA (post and message bodies are user content).
import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { World } from "./fixtures/world";

interface PersonJSON {
  ref: string;
  handle: string;
}

const sameOrigin = { "Sec-Fetch-Site": "same-origin" };

// setup: alice – bob – carol and alice – dave; carol posted once for her
// direct friends only (bob) and once for everyone she is connected to.
async function setup(request: APIRequestContext, baseURL: string | undefined): Promise<void> {
  const world = new World(request, baseURL ?? "");
  await world.reset();
  await world.seed("dm-1");
}

async function loginAs(page: Page, baseURL: string | undefined, handle: string): Promise<void> {
  await page.context().clearCookies();
  await new World(page.request, baseURL ?? "").login(handle);
}

async function getJSON<T>(req: APIRequestContext, path: string): Promise<T> {
  const res = await req.get(path, { headers: sameOrigin });
  expect(res.status(), path).toBe(200);
  return (await res.json()) as T;
}

async function friendRef(req: APIRequestContext, handle: string): Promise<string> {
  const ref = (await getJSON<{ items: PersonJSON[] }>(req, "/api/friends")).items.find((f) => f.handle === handle)?.ref;
  expect(ref, handle).toBeDefined();
  return ref ?? "";
}

test("a private conversation, its badge, and a shared post kept to its audience", async ({ page, request, baseURL }) => {
  await setup(request, baseURL);

  // alice writes to bob from his page.
  await loginAs(page, baseURL, "alice");
  await page.goto(`/people/${await friendRef(page.request, "bob")}`);
  await page.getByTestId("person-message").click();
  await expect(page).toHaveURL(/\/messages\/[^/]+$/);
  const box = page.getByTestId("composer-body");
  await box.fill("Hello Bob, it's Alice");
  await box.press("Enter");
  await expect(page.getByTestId("message-body")).toHaveText("Hello Bob, it's Alice");
  await expect(box).toHaveValue("");

  // bob sees the badge, reads the message, and the badge goes.
  await loginAs(page, baseURL, "bob");
  await page.goto("/");
  const badge = page.getByRole("navigation").getByTestId("messages-badge");
  await expect(badge).toHaveAttribute("data-count", "1");
  // On a phone the count rides on the Messages tab.
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator(".tabbar").getByTestId("messages-badge")).toHaveAttribute("data-count", "1");
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.goto("/messages");
  const row = page.getByTestId("conversation-row");
  await expect(row).toHaveAttribute("data-unread", "1");
  await row.getByRole("link").click();
  await expect(page.getByTestId("message-body")).toHaveText("Hello Bob, it's Alice");
  await expect(badge).toHaveCount(0);

  // bob shares carol's friends-only post with alice, from the post itself.
  const carol = await friendRef(page.request, "carol");
  const posts = (await getJSON<{ items: { ref: string; body: string }[] }>(page.request, `/api/people/${carol}/posts`)).items;
  const close = posts.find((p) => p.body === "Only for my direct friends");
  expect(close).toBeDefined();
  await page.goto(`/p/${close?.ref ?? ""}`);
  await page.getByTestId("engage-share").first().click();
  const sheet = page.getByTestId("share-modal");
  await expect(sheet.getByTestId("post-preview")).toContainText("Only for my direct friends");
  await sheet.getByTestId("share-target").getByRole("checkbox").check();
  await sheet.getByTestId("share-note").fill("Have a look");
  await sheet.getByTestId("share-send").click();
  await expect(sheet.getByTestId("share-done")).toBeVisible();

  // alice gets the note and only a placeholder for the post.
  await loginAs(page, baseURL, "alice");
  await page.goto("/messages");
  await page.getByTestId("conversation-row").getByRole("link").click();
  const shared = page.getByTestId("message").last();
  await expect(shared.getByTestId("message-body")).toHaveText("Have a look");
  await expect(shared.getByTestId("message-post-unavailable")).toBeVisible();
  await expect(page.getByTestId("conversation-thread")).not.toContainText("Only for my direct friends");
  expect(JSON.stringify(await getJSON(page.request, "/api/messages"))).not.toContain("Only for my direct friends");
});

test("a group: created from several connections, joined from the invitation", async ({ page, request, baseURL }) => {
  await setup(request, baseURL);

  await loginAs(page, baseURL, "bob");
  await page.goto("/messages/new");
  for (const h of ["alice", "carol"]) {
    await page.locator(`[data-testid="picker-person"][data-handle="${h}"]`).getByRole("checkbox").check();
  }
  await page.getByTestId("start-title").fill("Weekend plans");
  await page.getByTestId("start-submit").click();
  await expect(page.getByTestId("conversation-name")).toHaveText("Weekend plans");
  await expect(page.getByTestId("message-event")).toHaveAttribute("data-event", "created");
  await page.getByTestId("composer-body").fill("Who's in?");
  await page.getByTestId("composer-body").press("Enter");
  await expect(page.getByTestId("message-body")).toHaveText("Who's in?");

  // carol is invited: she sees who invited her, not the members, until she joins.
  await loginAs(page, baseURL, "carol");
  await page.goto("/messages");
  const inv = page.getByTestId("invitation-row");
  await expect(inv).toContainText("@bob");
  await inv.getByTestId("invitation-join").click();
  await expect(page.getByTestId("conversation-name")).toHaveText("Weekend plans");
  await expect(page.getByTestId("message-body")).toHaveText("Who's in?");
  await page.getByTestId("composer-body").fill("Me!");
  await page.getByTestId("composer-body").press("Enter");
  await expect(page.getByTestId("message-body").last()).toHaveText("Me!");
  await page.getByTestId("conversation-options").locator("summary").click();
  await expect(page.getByTestId("conversation-member")).toHaveCount(1); // bob; alice has not joined yet

  // dave, outside the group, sees nothing of it.
  await loginAs(page, baseURL, "dave");
  await page.goto("/messages");
  await expect(page.getByTestId("messages-empty")).toBeVisible();
});
