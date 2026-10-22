// Feed feature: view-model mapping, and the feed page against a stubbed API —
// a session started with the saved preset and continued by cursor, the preset
// switch (restarts and saves), "N new posts above" polling, collapsed groups
// expanded in place, a restart on feed_session_expired, the empty state and
// the reader's format.
import { QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { components } from '../../api/schema.gen';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import { asRef, type FeedPageVM } from '../../vm/types';
import { resetEngageStore } from '../engage/store';
import { me, postJSON } from '../post/testdata';
import { toPostVM } from '../post/vm';
import { resetSeenQueue } from '../seen/tracker';
import { fetchWhy } from './api';
import FeedPage, { NEW_ABOVE_MS } from './FeedPage';
import { addOwnPosts, countPosts, expandInPages, toFeedItemVM, toFeedPageVM, toWhyVM } from './vm';

interface Call {
  method: string;
  path: string;
  query: Record<string, string>;
  body: unknown;
}

function json(status: number, body: unknown): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function stubAPI(handler: (c: Call) => Response | undefined): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const text = await req.clone().text();
      const u = new URL(req.url);
      const c = {
        method: req.method,
        path: u.pathname.replace(/^\/api/, ''),
        query: Object.fromEntries(u.searchParams),
        body: text ? (JSON.parse(text) as unknown) : undefined,
      };
      calls.push(c);
      return handler(c) ?? json(404, { error: 'not_found' });
    }),
  );
  return calls;
}

type S = components['schemas'];

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

const post = (ref: string, tier: number, body = `post ${ref}`): S['PostView'] =>
  postJSON({
    ref,
    tier,
    body,
    mine: false,
    author: {
      ref: `u-${ref}`,
      handle: 'bob',
      display_name: 'Bob',
      inviter: { kind: 'user', handle: 'carol' },
      chain: { dist: tier, hidden_middle: 0, first_hop_plus: false },
    },
  }) as S['PostView'];

// The session view carries a settings summary; the format comes from GET /me.
const session = () => json(200, { authenticated: true, kind: 'user', csrf: 'c', me });

function feedAPI(extra: (c: Call) => Response | undefined = () => undefined, prefs: Record<string, unknown> = {}): Call[] {
  return stubAPI((c) => {
    const r = extra(c);
    if (r) return r;
    if (c.path === '/session') return session();
    if (c.path === '/me') return json(200, { ...me, settings: { ...me.settings, ui_prefs: prefs } });
    if (c.path === '/feed' && c.query.cursor === 'next-1') {
      return json(200, {
        session: 's1',
        preset: 'balanced',
        items: [
          { type: 'post', post: post('b', 1) },
          { type: 'tier_end', tier: 1 },
          { type: 'tier_start', tier: 3 },
          { type: 'collapsed', tier: 3, collapsed: { count: 2, token: 'tok', reason: 'low_trust_distant' } },
          { type: 'tier_end', tier: 3 },
          { type: 'caught_up' },
        ],
      });
    }
    if (c.path === '/feed') {
      return json(200, {
        session: c.query.preset === 'latest' ? 's2' : 's1',
        preset: c.query.preset ?? 'balanced',
        items: [{ type: 'tier_start', tier: 1 }, { type: 'post', post: post('a', 1) }],
        next: 'next-1',
      });
    }
    if (c.path === '/feed/new') return json(200, { count: 0, tier: 0 });
    return undefined;
  });
}

function renderFeed(qc = makeQueryClient()) {
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <FeedPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

const feedCalls = (calls: Call[]) => calls.filter((c) => c.path === '/feed').map((c) => c.query);

beforeEach(() => {
  resetEngageStore();
  resetSeenQueue();
  window.scrollTo = vi.fn();
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('feed view-models', () => {
  it('maps pages, markers, threads and collapsed groups', () => {
    const vm = toFeedPageVM({
      session: 's',
      preset: 'latest',
      items: [
        { type: 'tier_start', tier: 2 },
        { type: 'post', post: post('a', 2) },
        { type: 'thread', thread: [post('t1', 2), post('t2', 2)] },
        { type: 'collapsed', tier: 3, collapsed: { count: 4, token: 'k', reason: 'low_trust_distant' } },
        { type: 'tier_end', tier: 2 },
        { type: 'caught_up' },
      ],
    });
    expect(vm.next).toBeUndefined();
    expect(vm.items.map((i) => i.type)).toEqual(['tier_start', 'post', 'thread', 'collapsed', 'tier_end', 'caught_up']);
    expect(vm.items[1]).toMatchObject({ type: 'post', post: { ref: 'a', tier: 2 } });
    expect(vm.items[2]).toMatchObject({ type: 'thread', parts: [{ ref: 't1' }, { ref: 't2' }] });
    expect(vm.items[3]).toEqual({ type: 'collapsed', tier: 3, count: 4, token: 'k' });
  });

  it('drops items it cannot render', () => {
    expect(toFeedItemVM({ type: 'post' })).toBeUndefined();
    expect(toFeedItemVM({ type: 'thread', thread: [] })).toBeUndefined();
    expect(toFeedItemVM({ type: 'collapsed', tier: 3 })).toBeUndefined();
  });

  it('expands a group in place and counts posts', () => {
    const pages: FeedPageVM[] = [
      toFeedPageVM({ session: 's', preset: 'latest', items: [{ type: 'post', post: post('a', 3) }] }),
      toFeedPageVM({
        session: 's',
        preset: 'latest',
        items: [{ type: 'collapsed', tier: 3, collapsed: { count: 2, token: 'k', reason: 'low_trust_distant' } }, { type: 'caught_up' }],
      }),
    ];
    const items = toFeedPageVM({ session: 's', preset: 'latest', items: [{ type: 'post', post: post('z1', 3) }, { type: 'post', post: post('z2', 3) }] }).items;
    const next = expandInPages(pages, 'k', items);
    expect(next[0]).toBe(pages[0]);
    expect(next[1]?.items.map((i) => i.type)).toEqual(['post', 'post', 'caught_up']);
    expect(countPosts(pages)).toBe(1);
    expect(countPosts(next)).toBe(3);
    expect(expandInPages(pages, 'other', items)[1]).toBe(pages[1]);
  });

  it('maps and fetches "why"', async () => {
    const why = {
      tier: 2,
      via: { dist: 2, first_hop: { ref: 'u2', handle: 'bob' }, hidden_middle: 0, first_hop_plus: false },
      posted_at: '2026-01-01T00:00:00Z',
      preset: 'balanced' as const,
      shift_minutes: 240,
      reasons: ['network_likes' as const],
    };
    expect(toWhyVM(why)).toEqual({
      tier: 2,
      via: { dist: 2, firstHop: { ref: asRef('u2'), handle: 'bob' }, middle: [], hiddenMiddle: 0, firstHopPlus: false },
      postedAt: new Date('2026-01-01T00:00:00Z'),
      preset: 'balanced',
      shiftMinutes: 240,
      reasons: ['network_likes'],
    });
    const calls = stubAPI((c) => (c.path === '/posts/p9/why' ? json(200, why) : undefined));
    expect((await fetchWhy('p9')).shiftMinutes).toBe(240);
    expect(calls.map((c) => c.path)).toEqual(['/posts/p9/why']);
  });
});

describe('feed page', () => {
  it('starts a session with the saved preset and continues it by cursor', async () => {
    const calls = feedAPI();
    renderFeed();
    await screen.findByText('post a');
    expect(screen.getByTestId('feed-layout').dataset.format).toBe('cards');
    expect(screen.getByTestId('feed-preset-balanced').getAttribute('aria-pressed')).toBe('true');
    fireEvent.click(screen.getByTestId('feed-more'));
    await screen.findByText('post b');
    expect(feedCalls(calls)).toEqual([{}, { cursor: 'next-1' }]);
    // Both pages form one list with no tier headers; only "caught up" is shown.
    expect(screen.queryByTestId('feed-tier')).toBeNull();
    expect(screen.getAllByTestId('integrity-tier-header').map((h) => h.textContent)).toEqual([tr('integrity:tier.caughtUp')]);
    expect(screen.queryByTestId('feed-more')).toBeNull();
  });

  it('expands a collapsed group in place', async () => {
    const calls = feedAPI((c) =>
      c.path === '/feed/expand'
        ? json(200, { items: [{ type: 'post', post: post('z1', 3, 'zoe one') }, { type: 'post', post: post('z2', 3, 'zoe two') }] })
        : undefined,
    );
    renderFeed();
    await screen.findByText('post a');
    fireEvent.click(screen.getByTestId('feed-more'));
    fireEvent.click(await screen.findByTestId('feed-expand'));
    await screen.findByText('zoe two');
    expect(screen.queryByTestId('feed-collapsed')).toBeNull();
    expect(calls.find((c) => c.path === '/feed/expand')?.query).toEqual({ token: 'tok' });
  });

  it('offers Top and starts with a saved Top', async () => {
    const calls = stubAPI((c) => {
      if (c.path === '/session') return json(200, { authenticated: true, kind: 'user', csrf: 'c', me: { ...me, settings: { ...me.settings, rank_preset: 'top' } } });
      if (c.path === '/me') return json(200, { ...me, settings: { ...me.settings, rank_preset: 'top', ui_prefs: {} } });
      if (c.path === '/feed') return json(200, { session: 's1', preset: 'top', items: [{ type: 'post', post: post('a', 1) }] });
      if (c.path === '/feed/new') return json(200, { count: 0, tier: 0 });
      return undefined;
    });
    renderFeed();
    await screen.findByText('post a');
    await waitFor(() => {
      expect(screen.getByTestId('feed-preset-top').getAttribute('aria-pressed')).toBe('true');
    });
    expect(screen.getByTestId('feed-preset-top').textContent).toBe(tr('feed:preset.top'));
    fireEvent.click(within(screen.getByTestId('feed-preset-hint')).getByRole('button', { name: tr('feed:preset.hintLabel') }));
    expect(screen.getByText(tr('feed:preset.hint', { preset: 'top' }))).toBeTruthy();
    expect(feedCalls(calls)).toEqual([{}]);
  });

  it('switches to Latest: a new session in that order, saved as the default', async () => {
    const calls = feedAPI((c) => (c.method === 'PATCH' && c.path === '/me/settings' ? json(200, me.settings) : undefined));
    renderFeed();
    await screen.findByText('post a');
    fireEvent.click(screen.getByTestId('feed-preset-latest'));
    await waitFor(() => {
      expect(feedCalls(calls)).toEqual([{}, { preset: 'latest' }]);
    });
    await waitFor(() => {
      expect(calls.some((c) => c.method === 'PATCH' && (c.body as { rank_preset?: string }).rank_preset === 'latest')).toBe(true);
    });
    expect(screen.getByTestId('feed-preset-latest').getAttribute('aria-pressed')).toBe('true');
    // Load more follows the new session's cursor only.
    fireEvent.click(await screen.findByTestId('feed-more'));
    await waitFor(() => {
      expect(feedCalls(calls).at(-1)).toEqual({ cursor: 'next-1' });
    });
  });

  it('restarts when the server no longer keeps the session', async () => {
    const calls = feedAPI((c) => (c.path === '/feed' && c.query.cursor ? json(409, { error: 'feed_session_expired' }) : undefined));
    renderFeed();
    await screen.findByText('post a');
    fireEvent.click(screen.getByTestId('feed-more'));
    await waitFor(() => {
      expect(feedCalls(calls)).toEqual([{}, { cursor: 'next-1' }, {}]);
    });
    await screen.findByText('post a');
    expect(screen.getByTestId('feed-page').dataset.sessionGen).toBe('1');
  });

  it('polls for new posts above every minute and starts over on request', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let fresh = 0;
    const calls = feedAPI((c) => (c.path === '/feed/new' ? json(200, { count: fresh, tier: fresh ? 1 : 0 }) : undefined));
    renderFeed();
    await screen.findByText('post a');
    expect(calls.filter((c) => c.path === '/feed/new')).toHaveLength(0);
    fresh = 3;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(NEW_ABOVE_MS + 100);
    });
    const banner = await screen.findByTestId('feed-new-above');
    expect(calls.find((c) => c.path === '/feed/new')?.query).toEqual({ session: 's1' });
    expect(banner.textContent).toBe(tr('feed:newAbove.banner', { count: 3 }));
    fresh = 0;
    fireEvent.click(banner);
    await waitFor(() => {
      expect(feedCalls(calls)).toEqual([{}, {}]);
    });
    await waitFor(() => {
      expect(screen.queryByTestId('feed-new-above')).toBeNull();
    });
  });

  it("shows the reader's just-published post on top (D16)", async () => {
    feedAPI();
    const qc = makeQueryClient();
    addOwnPosts(qc, [toPostVM({ ...post('mine', 1, 'my new post'), mine: true })]);
    renderFeed(qc);
    await screen.findByText('post a');
    const posts = screen.getAllByTestId('feed-post');
    expect(posts[0]?.textContent).toContain('my new post');
    expect(screen.getAllByText('my new post')).toHaveLength(1);
  });

  it('says so when there is nothing yet', async () => {
    feedAPI((c) => (c.path === '/feed' ? json(200, { session: 's1', preset: 'balanced', items: [{ type: 'caught_up' }] }) : undefined));
    renderFeed();
    expect((await screen.findByTestId('feed-empty')).textContent).toBe(tr('feed:page.empty'));
    expect((await screen.findByTestId('integrity-tier-header')).textContent).toBe(tr('integrity:tier.caughtUp'));
  });

  it('shows an error with a retry', async () => {
    let fail = true;
    feedAPI((c) => (c.path === '/feed' && fail ? json(429, { error: 'rate_limited', retry_after_s: 5 }) : undefined));
    renderFeed();
    const err = await screen.findByTestId('feed-error');
    fail = false;
    fireEvent.click(within(err).getByRole('button'));
    await screen.findByText('post a');
  });

  it("uses the reader's format", async () => {
    feedAPI(undefined, { format: 'compact' });
    renderFeed();
    await screen.findByText('post a');
    expect(screen.getByTestId('feed-layout').dataset.format).toBe('compact');
  });
});
