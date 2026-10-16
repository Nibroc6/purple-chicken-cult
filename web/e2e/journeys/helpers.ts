// Shared pieces of the INT-0 journeys (plan §7 "Required e2e journeys"): a
// `test` whose every page fails on a CSP or Trusted Types violation (or an
// uncaught page error), an API actor driving one signed-in session, and the
// pseudo-locale and truncation probes. Selectors avoid English text, so every
// journey runs in en and in the pseudo-locale en-XA.
import { test as base, expect, type APIRequestContext, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { World } from '../fixtures/world';

export const sameOrigin = { 'Sec-Fetch-Site': 'same-origin' };
export const notFound = '{"error":"not_found"}\n';
// at matches @handle in UI text in en and en-XA (the pseudo-locale pads the
// literal "@" of messages like "invited by @{handle}" to "@~~~").
export function at(handle: string): RegExp {
  return new RegExp(`@~*${handle}\\b`);
}

export const garbageRef = 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA';

// watchSecurity records CSP and Trusted Types violations and uncaught errors of
// page into problems (plan §7: a violation listener on every test).
export async function watchSecurity(page: Page, problems: string[]): Promise<void> {
  page.on('console', (m) => {
    const t = m.text();
    if (m.type() === 'error' && /CSP violation|Content Security Policy|Trusted ?Type/i.test(t)) problems.push(t);
  });
  page.on('pageerror', (e) => problems.push(`pageerror: ${e.message}`));
  await page.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => {
      console.error(`CSP violation: ${e.violatedDirective} ${e.blockedURI}`);
    });
  });
}

interface Fixtures {
  securityProblems: string[];
  // newPage opens a watched page in a fresh context of browser (another user's
  // browser, e.g. the invitee's camera browser) with this test's locale.
  newWatchedContext: (browser?: Browser) => Promise<{ ctx: BrowserContext; page: Page }>;
}

export const test = base.extend<Fixtures>({
  securityProblems: [
    async ({ page }, use) => {
      const problems: string[] = [];
      await watchSecurity(page, problems);
      await use(problems);
      expect(problems, 'CSP / Trusted Types violations or page errors').toEqual([]);
    },
    { auto: true },
  ],
  newWatchedContext: async ({ browser, baseURL, locale, securityProblems }, use) => {
    const opened: BrowserContext[] = [];
    await use(async (b?: Browser) => {
      const ctx = await (b ?? browser).newContext({ baseURL: baseURL ?? '', ...(locale ? { locale } : {}) });
      opened.push(ctx);
      const page = await ctx.newPage();
      await watchSecurity(page, securityProblems);
      return { ctx, page };
    });
    for (const c of opened) await c.close().catch(() => undefined);
  },
});

export { expect };

// Actor is one signed-in session driven through the API (CSRF attached).
export class Actor {
  constructor(
    readonly req: APIRequestContext,
    readonly origin: string,
    readonly csrf: string,
  ) {}

  // login replaces req's session with a fresh one for handle (test hook).
  static async login(req: APIRequestContext, origin: string, handle: string): Promise<Actor> {
    return new Actor(req, origin, await new World(req, origin).login(handle));
  }

  headers(): Record<string, string> {
    return { Origin: this.origin, ...sameOrigin, 'X-PCC-CSRF': this.csrf };
  }

  async get<T>(path: string): Promise<T> {
    const res = await this.req.get(`/api${path}`, { headers: sameOrigin });
    expect(res.status(), path).toBe(200);
    return (await res.json()) as T;
  }

  async raw(path: string): Promise<{ status: number; body: string }> {
    const res = await this.req.get(`/api${path}`, { headers: sameOrigin });
    return { status: res.status(), body: await res.text() };
  }

  async send(method: 'POST' | 'PUT' | 'PATCH' | 'DELETE', path: string, data?: unknown): Promise<{ status: number; body: string }> {
    const res = await this.req.fetch(`/api${path}`, { method, headers: this.headers(), ...(data === undefined ? {} : { data }) });
    return { status: res.status(), body: await res.text() };
  }

  // feedPosts pages through a whole fresh feed session (preset as given).
  async feedPosts(preset = 'latest'): Promise<PostJSON[]> {
    const out: PostJSON[] = [];
    let path = `/feed?preset=${preset}`;
    for (let i = 0; i < 20; i++) {
      const page = await this.get<FeedJSON>(path);
      for (const it of page.items) out.push(...(it.post ? [it.post] : (it.thread ?? [])));
      if (!page.next) break;
      path = `/feed?cursor=${page.next}`;
    }
    return out;
  }

  async feedPost(body: string): Promise<PostJSON> {
    const p = (await this.feedPosts()).find((x) => x.body === body);
    if (!p) throw new Error(`no feed post "${body}"`);
    return p;
  }

  async friend(handle: string): Promise<PersonJSON> {
    const f = (await this.get<{ items: PersonJSON[] }>('/friends')).items.find((p) => p.handle === handle);
    if (!f) throw new Error(`no friend @${handle}`);
    return f;
  }
}

export interface PersonJSON {
  ref: string;
  handle: string;
  display_name?: string;
  relation?: string;
  inviter?: { kind: string; handle?: string };
}

export interface PostJSON {
  ref: string;
  body: string;
  tier: number;
  audience: string;
  audience_forced?: boolean;
  reply_count: number;
  author: { ref: string; handle: string; inviter?: { kind: string; handle?: string }; chain?: { dist: number; first_hop?: { handle: string } } };
  context?: { ref: string; kind: string; author_handle: string }[];
  likes: { in_network: number; names?: { handle: string }[]; others: number; by_me: boolean };
  thread?: { index: number; total: number };
}

export interface FeedJSON {
  session: string;
  preset: string;
  items: { type: string; tier?: number; post?: PostJSON; thread?: PostJSON[] }[];
  next?: string;
}

// actor opens a new cookie jar signed in as handle.
export async function actor(pw: { request: { newContext: (o: { baseURL: string }) => Promise<APIRequestContext> } }, origin: string, handle: string): Promise<Actor> {
  return Actor.login(await pw.request.newContext({ baseURL: origin }), origin, handle);
}

// signInPage replaces page's session with a fresh one for handle.
export async function signInPage(page: Page, origin: string, handle: string): Promise<Actor> {
  await page.context().clearCookies();
  return Actor.login(page.request, origin, handle);
}

// seedWorld resets the run's database and loads a fixture.
export async function seedWorld(request: APIRequestContext, origin: string, fixture: string): Promise<World> {
  const world = new World(request, origin);
  await world.reset();
  await world.seed(fixture);
  return world;
}

// untranslated lists visible UI text that is not pseudo-localized: text nodes
// (and the alt / title / aria-label / placeholder attributes) without the
// pseudo-locale's [ … ] markers that hold a run of two or more plain ASCII
// letters once @handles are taken out. The pseudo-locale turns every literal
// ASCII letter into an accented one, so such text can only come from a literal
// string, a missing key or user content outside a data-user-content container. <time> and <code> hold Intl dates and opaque
// refs, not catalog text.
export function untranslated(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const skip = '[data-user-content], [data-intl], script, style, noscript, time, code, input, textarea';
    // A catalog message is bracketed ("[Ŧêẋŧ {arg}]"); its interpolated data
    // (dates, key IDs, counts) may hold plain letters.
    const plain = (s: string) => !/[[\]]/.test(s) && /[A-Za-z]{2,}/.test(s.replace(/@[A-Za-z0-9_]+/g, ''));
    const visible = (el: Element) => el.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true });
    const where = (el: Element) => {
      const id = el.closest('[data-testid]')?.getAttribute('data-testid');
      return `${el.tagName.toLowerCase()}${id ? `@${id}` : ''}`;
    };
    const out: string[] = [];
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let n = walker.nextNode(); n; n = walker.nextNode()) {
      const el = n.parentElement;
      const text = n.textContent ?? '';
      if (!el || el.closest(skip) || !plain(text)) continue;
      if (el.tagName === 'OPTION' ? !visible(el.closest('select') ?? el) : !visible(el)) continue;
      out.push(`${where(el)}: ${text.trim()}`);
    }
    for (const el of document.body.querySelectorAll('[alt], [title], [aria-label], [placeholder]')) {
      if (el.closest('[data-user-content], [data-intl], time') || !visible(el)) continue;
      for (const a of ['alt', 'title', 'aria-label', 'placeholder']) {
        const v = el.getAttribute(a);
        if (v && plain(v)) out.push(`${where(el)}[${a}]: ${v}`);
      }
    }
    return out;
  });
}

// truncatedIntegrity lists integrity elements whose content overflows their box.
export function truncatedIntegrity(page: Page): Promise<string[]> {
  return page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>('.integrity, [data-testid^="integrity-"]')]
      .filter((el) => el.checkVisibility() && el.scrollWidth > el.clientWidth + 1)
      .map((el) => `${el.dataset.testid ?? el.className}: ${el.textContent}`),
  );
}
