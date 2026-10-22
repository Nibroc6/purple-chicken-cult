// Notifications: the SSE singleton (one EventSource, typed subscriptions,
// backoff and close with the last subscriber), the nav badge fed by hello /
// notification events, view-model mapping, and the page rendering every kind
// from the catalogs with ICU plurals, marking read up to the newest, refreshing
// on a live event, paging and muting a post.
import { QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { components } from '../../api/schema.gen';
import { backoffDelay, BACKOFF_MAX_MS, dispatch, resetSSE, subscribe, SSE_URL } from '../../api/sse';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import NotificationsPage from './NotificationsPage';
import { NotificationsBadge } from './slots';
import { getUnread, resetUnread } from './unread';
import { messageArgs, toNotificationPage, toNotificationVM } from './vm';

type S = components['schemas'];

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

// ---- a fake EventSource ----

class FakeES {
  static all: FakeES[] = [];
  url: string;
  closed = false;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  listeners = new Map<string, ((e: MessageEvent<string>) => void)[]>();
  constructor(url: string) {
    this.url = url;
    FakeES.all.push(this);
  }
  addEventListener(type: string, fn: (e: MessageEvent<string>) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }
  close() {
    this.closed = true;
  }
  emit(type: string, data: string) {
    for (const fn of this.listeners.get(type) ?? []) fn(new MessageEvent(type, { data }));
  }
  static last(): FakeES {
    const es = FakeES.all.at(-1);
    if (!es) throw new Error('no EventSource');
    return es;
  }
  static open(): FakeES[] {
    return FakeES.all.filter((e) => !e.closed);
  }
}

beforeEach(() => {
  FakeES.all = [];
  vi.stubGlobal('EventSource', FakeES);
  resetSSE();
  resetUnread();
});

afterEach(() => {
  resetSSE();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('sse', () => {
  it('opens one stream for all subscriptions and closes it with the last', () => {
    const a = vi.fn();
    const b = vi.fn();
    const offA = subscribe('hello', a);
    const offB = subscribe('notification', b);
    expect(FakeES.all).toHaveLength(1);
    const es = FakeES.last();
    expect(es.url).toBe(SSE_URL);
    es.emit('hello', '{"unread":2}');
    es.emit('notification', '{"unread":3}');
    es.emit('notification', 'not json');
    expect(a).toHaveBeenCalledWith({ unread: 2 });
    expect(b).toHaveBeenCalledTimes(1);
    // A type subscribed later is listened to on the open stream.
    const c = vi.fn();
    const offC = subscribe('handshake', c);
    es.emit('handshake', '{"state":"claimed"}');
    expect(c).toHaveBeenCalledWith({ state: 'claimed' });
    offA();
    offB();
    expect(es.closed).toBe(false);
    offC();
    expect(es.closed).toBe(true);
  });

  it('reconnects with backoff after an error, reset on open', () => {
    vi.useFakeTimers();
    vi.spyOn(Math, 'random').mockReturnValue(0.999);
    const off = subscribe('hello', vi.fn());
    FakeES.last().onerror?.();
    expect(FakeES.open()).toHaveLength(0);
    vi.advanceTimersByTime(999);
    expect(FakeES.all).toHaveLength(1);
    vi.advanceTimersByTime(2);
    expect(FakeES.all).toHaveLength(2);
    FakeES.last().onerror?.();
    vi.advanceTimersByTime(1500);
    expect(FakeES.all).toHaveLength(2); // second wait ≈ 2 s
    vi.advanceTimersByTime(600);
    expect(FakeES.all).toHaveLength(3);
    FakeES.last().onopen?.();
    FakeES.last().onerror?.();
    vi.advanceTimersByTime(1001);
    expect(FakeES.all).toHaveLength(4); // back to 1 s after a successful open
    off();
    expect(FakeES.open()).toHaveLength(0);
    // No reconnect once nobody listens.
    vi.advanceTimersByTime(BACKOFF_MAX_MS * 2);
    expect(FakeES.all).toHaveLength(4);
  });

  it('caps the backoff with jitter', () => {
    expect(backoffDelay(0, () => 0)).toBe(500);
    expect(backoffDelay(3, () => 0.999)).toBeLessThanOrEqual(8000);
    expect(backoffDelay(30, () => 0.999)).toBeLessThanOrEqual(BACKOFF_MAX_MS);
    expect(backoffDelay(30, () => 0)).toBe(BACKOFF_MAX_MS / 2);
  });
});

describe('badge', () => {
  it('shows the live unread count and hides at zero', () => {
    render(<NotificationsBadge />);
    expect(screen.queryByTestId('notifications-badge')).toBeNull();
    act(() => {
      FakeES.last().emit('hello', '{"unread":3}');
    });
    const b = screen.getByTestId('notifications-badge');
    expect(b.textContent).toBe('3');
    expect(b.getAttribute('aria-label')).toBe(tr('notifications:badge.label', { count: 3 }));
    act(() => {
      dispatch('notification', { unread: 120 });
    });
    expect(screen.getByTestId('notifications-badge').textContent).toBe(tr('notifications:badge.many'));
    act(() => {
      dispatch('notification', { unread: 0 });
    });
    expect(screen.queryByTestId('notifications-badge')).toBeNull();
  });
});

// ---- the page against a stubbed API ----

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

const at = '2026-01-01T00:00:00Z';
const nv = (id: string, kind: S['NotificationView']['kind'], over: Partial<S['NotificationView']> = {}): S['NotificationView'] => ({
  id,
  kind,
  count: 1,
  at,
  read: false,
  ...over,
});
const carol = { ref: 'r-carol', handle: 'carol' };

function renderPage() {
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <MemoryRouter>
        <NotificationsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('view-models', () => {
  it('maps views and keeps only code data', () => {
    const vm = toNotificationVM(
      nv('n1', 'friend_security', { person: carol, data: { reason: 'passkey_added', n: 2, prose: 'Hello there' } }),
    );
    expect(vm.person).toEqual({ ref: 'r-carol', handle: 'carol' });
    expect(vm.data).toEqual({ reason: 'passkey_added', n: 2 });
    expect(vm.at.toISOString()).toBe('2026-01-01T00:00:00.000Z');
    expect(messageArgs(vm)).toMatchObject({ who: 'person', reason: 'passkey_added', count: 1, others: 0 });
    const p = toNotificationPage({ items: [nv('n2', 'post_referenced', { count: 4, post: 'p1' })], unread: 1, next: 'c' });
    expect(p.next).toBe('c');
    expect(messageArgs(p.items[0] ?? vm)).toMatchObject({ who: 'none', count: 4, others: 3, reason: 'other' });
  });
});

describe('NotificationsPage', () => {
  it('renders every kind from the catalogs and marks read up to the newest', async () => {
    const items = [
      nv('n1', 'friend_request', { person: carol }),
      nv('n2', 'request_accepted', { person: carol }),
      nv('n3', 'post_referenced', { person: carol, count: 3, post: 'p1' }),
      nv('n4', 'post_referenced', { count: 2, post: 'p2' }),
      nv('n5', 'mention', { person: carol, post: 'p3' }),
      nv('n6', 'friend_security', { person: carol, data: { reason: 'passkey_removed' } }),
      nv('n7', 'friend_security', { data: { reason: 'account_deleted' } }),
      nv('n8', 'invite_redeemed', { person: carol }),
      nv('n9', 'invitee_action', { data: { action: 'suspend_user' }, read: true }),
      nv('n10', 'recovery_approval', { person: carol, read: true }),
      nv('n11', 'account_recovery', { data: { reason: 'recovery_pending' }, read: true }),
    ];
    const calls = stubAPI((c) => {
      if (c.path === '/notifications' && c.method === 'GET') return json(200, { items, unread: 8 });
      if (c.path === '/notifications/read') return json(204, null);
      return undefined;
    });
    renderPage();
    const list = await screen.findAllByTestId('notification');
    expect(list).toHaveLength(11);
    const text = (i: number) => within(list[i] as HTMLElement).getByText((_, el) => el?.tagName === 'SPAN' && el.parentElement?.className === 'notification-text').textContent;
    expect(text(0)).toBe(tr('notifications:kind.friend_request'));
    expect(text(2)).toBe('and 2 others replied to your post');
    expect(text(3)).toBe('2 replies to your post from your network');
    expect(text(5)).toBe('removed a passkey from their account');
    expect(text(6)).toBe('A friend deleted their account');
    expect(text(8)).toBe('A moderator suspended someone you invited');
    expect(text(10)).toMatch(/^A recovery of your account was approved/);
    expect(within(list[0] as HTMLElement).getByTestId('notification-person').getAttribute('href')).toBe('/people/r-carol');
    expect(within(list[0] as HTMLElement).getByTestId('notification-person').textContent).toBe('@carol');
    expect(within(list[4] as HTMLElement).getByTestId('notification-post').getAttribute('href')).toBe('/p/p3');
    expect(list.map((l) => l.dataset.read)).toEqual([...Array<string>(8).fill('false'), 'true', 'true', 'true']);
    await waitFor(() => {
      expect(calls.filter((c) => c.path === '/notifications/read').map((c) => c.body)).toEqual([{ up_to: 'n1' }]);
    });
    await waitFor(() => {
      expect(getUnread()).toBe(0);
    });
  });

  it('marks nothing when nothing is unread; shows the empty state', async () => {
    const calls = stubAPI((c) => (c.path === '/notifications' ? json(200, { items: [], unread: 0 }) : undefined));
    renderPage();
    expect(await screen.findByTestId('notifications-empty')).toBeTruthy();
    expect(calls.some((c) => c.path === '/notifications/read')).toBe(false);
  });

  it('pages with the cursor and refreshes on a live notification', async () => {
    let fresh = false;
    const calls = stubAPI((c) => {
      if (c.path !== '/notifications') return c.path === '/notifications/read' ? json(204, null) : undefined;
      if (c.query.cursor === 'c1') return json(200, { items: [nv('old', 'mention', { person: carol, post: 'p', read: true })], unread: 0 });
      return json(200, {
        items: [...(fresh ? [nv('new', 'friend_request', { person: carol })] : []), nv('n1', 'request_accepted', { person: carol, read: true })],
        unread: fresh ? 1 : 0,
        next: 'c1',
      });
    });
    renderPage();
    expect(await screen.findAllByTestId('notification')).toHaveLength(1);
    fireEvent.click(screen.getByTestId('notifications-more'));
    await waitFor(() => {
      expect(screen.getAllByTestId('notification')).toHaveLength(2);
    });
    expect(screen.queryByTestId('notifications-more')).toBeNull();
    // A count that drops (read elsewhere) does not refetch.
    const gets = () => calls.filter((c) => c.path === '/notifications').length;
    const before = gets();
    act(() => {
      dispatch('notification', { unread: 0 });
    });
    await new Promise((r) => setTimeout(r, 50));
    expect(gets()).toBe(before);
    fresh = true;
    act(() => {
      dispatch('notification', { unread: 1 });
    });
    await waitFor(() => {
      expect(screen.getAllByTestId('notification')[0]?.dataset.kind).toBe('friend_request');
    });
    await waitFor(() => {
      expect(calls.filter((c) => c.path === '/notifications/read').map((c) => c.body)).toEqual([{ up_to: 'new' }]);
    });
  });

  it('mutes and unmutes replies to a post', async () => {
    const calls = stubAPI((c) => {
      if (c.path === '/notifications') return json(200, { items: [nv('n1', 'post_referenced', { post: 'p1', read: true })], unread: 0 });
      if (c.path === '/posts/p1/notify-mute') return json(204, null);
      return undefined;
    });
    renderPage();
    const btn = await screen.findByTestId('notification-mute');
    expect(btn.textContent).toBe(tr('notifications:mute.action'));
    fireEvent.click(btn);
    await waitFor(() => {
      expect(screen.getByTestId('notification-mute').dataset.muted).toBe('true');
    });
    fireEvent.click(screen.getByTestId('notification-mute'));
    await waitFor(() => {
      expect(screen.getByTestId('notification-mute').dataset.muted).toBe('false');
    });
    expect(calls.filter((c) => c.path === '/posts/p1/notify-mute').map((c) => c.method)).toEqual(['PUT', 'DELETE']);
  });

  it('shows the error from the catalogs', async () => {
    stubAPI(() => json(429, { error: 'rate_limited', retry_after_s: 3 }));
    renderPage();
    expect((await screen.findByRole('alert')).textContent).toBe(tr('errors:rate_limited'));
  });
});
