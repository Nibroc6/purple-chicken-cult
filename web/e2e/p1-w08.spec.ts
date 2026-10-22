// P1-W08 end to end: the private save (the button on a post, the saves page,
// nothing about it visible to anyone else, unsave keeping the post listed until
// the page reloads) and the Top order (served, saved as the default, explained
// by "why"). Selectors avoid English text so the spec runs in en and en-XA
// (post bodies are user content).
import {
  expect,
  test,
  type APIRequestContext,
  type Page,
  type Response,
} from "@playwright/test";
import { World } from "./fixtures/world";

interface PostJSON {
  ref: string;
  body: string;
  saved_by_me?: boolean;
  likes: { in_network: number; others: number; by_me: boolean };
}

interface PersonJSON {
  ref: string;
  handle: string;
}

const sameOrigin = { "Sec-Fetch-Site": "same-origin" };

// setup resets the run and loads the fixture: alice – bob – carol; bob posted
// twice, carol once (liked by bob).
async function setup(
  request: APIRequestContext,
  baseURL: string | undefined,
): Promise<void> {
  const world = new World(request, baseURL ?? "");
  await world.reset();
  await world.seed("p1-w08");
}

async function loginAs(
  page: Page,
  baseURL: string | undefined,
  handle: string,
): Promise<string> {
  await page.context().clearCookies();
  return new World(page.request, baseURL ?? "").login(handle);
}

async function getJSON<T>(req: APIRequestContext, path: string): Promise<T> {
  const res = await req.get(path, { headers: sameOrigin });
  expect(res.status(), path).toBe(200);
  return (await res.json()) as T;
}

// postOf finds one of handle's posts, as the viewer sees it, by its body
// (handle is the viewer's friend, or the viewer).
async function postOf(
  req: APIRequestContext,
  handle: string,
  body: string,
): Promise<PostJSON> {
  const ref =
    handle === "me"
      ? (await getJSON<{ ref: string }>(req, "/api/me")).ref
      : ((
          await getJSON<{ items: PersonJSON[] }>(req, "/api/friends")
        ).items.find((f) => f.handle === handle)?.ref ?? "");
  expect(ref, handle).not.toBe("");
  const p = (
    await getJSON<{ items: PostJSON[] }>(req, `/api/people/${ref}/posts`)
  ).items.find((x) => x.body === body);
  expect(p, body).toBeDefined();
  return p as PostJSON;
}

const isFeedPage = (r: Response) => new URL(r.url()).pathname === "/api/feed";

test("a save is private: listed on the saves page, invisible to everyone else", async ({
  page,
  request,
  baseURL,
}) => {
  await setup(request, baseURL);
  await loginAs(page, baseURL, "bob");
  const before = await postOf(page.request, "me", "A post worth keeping");

  await loginAs(page, baseURL, "alice");
  const keep = await postOf(page.request, "bob", "A post worth keeping");
  expect(keep.saved_by_me ?? false).toBe(false);
  await page.goto(`/p/${keep.ref}/likes`);
  const save = page.getByTestId("engage-save");
  await expect(save).toHaveAttribute("aria-pressed", "false");
  const put = page.waitForResponse(
    (r) => r.request().method() === "PUT" && r.url().endsWith("/save"),
  );
  await save.click();
  expect((await put).status()).toBe(204);
  await expect(save).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByTestId("engage-likes")).toHaveAttribute(
    "data-count",
    "0",
  );

  await page.goto("/saves");
  await expect(page.getByTestId("saves-item")).toHaveCount(1);
  await expect(page.getByTestId("saves-item")).toHaveAttribute(
    "data-ref",
    keep.ref,
  );
  await expect(
    page.getByTestId("saves-item").getByTestId("engage-save"),
  ).toHaveAttribute("aria-pressed", "true");

  // The author (and anyone else) sees exactly what they saw before.
  await loginAs(page, baseURL, "bob");
  const after = await postOf(page.request, "me", "A post worth keeping");
  expect(after.saved_by_me ?? false).toBe(false);
  expect(after.likes).toEqual(before.likes);
  expect(
    (await getJSON<{ items: PostJSON[] }>(page.request, "/api/saves")).items,
  ).toHaveLength(0);

  // Unsave on the saves page: the post stays listed until the page reloads.
  await loginAs(page, baseURL, "alice");
  await page.goto("/saves");
  const del = page.waitForResponse(
    (r) => r.request().method() === "DELETE" && r.url().endsWith("/save"),
  );
  await page.getByTestId("saves-item").getByTestId("engage-save").click();
  expect((await del).status()).toBe(204);
  await expect(page.getByTestId("saves-item")).toHaveCount(1);
  await expect(
    page.getByTestId("saves-item").getByTestId("engage-save"),
  ).toHaveAttribute("aria-pressed", "false");
  await page.reload();
  await expect(page.getByTestId("saves-empty")).toBeVisible();
});

test('Top is served, becomes the saved order and is what "why" explains', async ({
  page,
  request,
  baseURL,
}) => {
  await setup(request, baseURL);
  await loginAs(page, baseURL, "alice");
  const first = page.waitForResponse(isFeedPage);
  await page.goto("/");
  expect((await first).status()).toBe(200);
  await expect(page.getByTestId("feed-preset-top")).toHaveAttribute(
    "aria-pressed",
    "false",
  );

  const top = page.waitForResponse(
    (r) =>
      isFeedPage(r) && new URL(r.url()).searchParams.get("preset") === "top",
  );
  const saved = page.waitForResponse(
    (r) =>
      r.request().method() === "PATCH" && r.url().endsWith("/api/me/settings"),
  );
  await page.getByTestId("feed-preset-top").click();
  const res = await top;
  expect(res.status()).toBe(200);
  expect(((await res.json()) as { preset: string }).preset).toBe("top");
  expect((await saved).status()).toBe(200);
  await expect(page.getByTestId("feed-preset-top")).toHaveAttribute(
    "aria-pressed",
    "true",
  );

  // The next visit starts in Top without asking for it.
  const again = page.waitForResponse(isFeedPage);
  await page.goto("/");
  const body = (await (await again).json()) as { preset: string };
  expect(body.preset).toBe("top");
  await expect(page.getByTestId("feed-preset-top")).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  const keep = await postOf(page.request, "bob", "A post worth keeping");
  expect(
    (
      await getJSON<{ preset: string }>(
        page.request,
        `/api/posts/${keep.ref}/why`,
      )
    ).preset,
  ).toBe("top");
});
