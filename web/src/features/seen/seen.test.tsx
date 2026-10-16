// Seen feature: the IntersectionObserver tracker (≥ 50 % for ≥ 1 s, only
// data-seen-ref elements, paused while the page is hidden), the batched queue
// (every 5 s, ≤ 50 per request, retry, flush on hide) and the recently-seen page.
import { QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import { postJSON } from '../post/testdata';
import SeenPage from './SeenPage';
import { slots } from './slots';
import { BATCH, flushSeen, FLUSH_MS, pendingSeen, queueSeen, resetSeenQueue } from './tracker';
import { SEEN_MS, useSeenTracker } from './useSeenTracker';

interface Call {
  method: string;
  path: string;
  body: unknown;
  keepalive: boolean;
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
        path: u.pathname.replace(/^\/api/, '') + u.search,
        body: text ? (JSON.parse(text) as unknown) : undefined,
        keepalive: req.keepalive,
      };
      calls.push(c);
      return handler(c) ?? json(404, { error: 'not_found' });
    }),
  );
  return calls;
}

const seenBodies = (calls: Call[]) => calls.filter((c) => c.path === '/seen').map((c) => (c.body as { posts: string[] }).posts);

// A controllable IntersectionObserver.
class FakeIO {
  static all: FakeIO[] = [];
  observed = new Set<Element>();
  constructor(private readonly cb: IntersectionObserverCallback) {
    FakeIO.all.push(this);
  }
  observe(el: Element) {
    this.observed.add(el);
  }
  unobserve(el: Element) {
    this.observed.delete(el);
  }
  disconnect() {
    this.observed.clear();
  }
  takeRecords() {
    return [];
  }
  fire(el: Element, ratio: number) {
    if (!this.observed.has(el)) return;
    this.cb([{ target: el, isIntersecting: ratio > 0, intersectionRatio: ratio } as IntersectionObserverEntry], this as never);
  }
}

function fire(el: Element, ratio: number) {
  act(() => {
    FakeIO.all.forEach((io) => {
      io.fire(el, ratio);
    });
  });
}

function setVisibility(v: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => v });
  act(() => {
    document.dispatchEvent(new Event('visibilitychange'));
  });
}

function Cards({ onSeen }: { onSeen: (ref: string) => void }) {
  const track = useSeenTracker(onSeen);
  return (
    <div>
      <article data-testid="card-a" data-seen-ref="a" ref={track}>
        <p data-testid="ctx-a" ref={track}>
          context row
        </p>
      </article>
      <article data-testid="card-b" data-seen-ref="b" ref={track} />
    </div>
  );
}

beforeEach(() => {
  FakeIO.all = [];
  vi.stubGlobal('IntersectionObserver', FakeIO);
  resetSeenQueue();
});

afterEach(() => {
  setVisibility('visible');
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('seen tracker', () => {
  it('counts ≥ 50 % for ≥ 1 s, once, and observes only data-seen-ref elements', () => {
    vi.useFakeTimers();
    const seen = vi.fn();
    render(<Cards onSeen={seen} />);
    const observed = [...(FakeIO.all[0]?.observed ?? [])].map((e) => e.getAttribute('data-testid'));
    expect(observed).toEqual(['card-a', 'card-b']);

    fire(screen.getByTestId('card-a'), 0.6);
    act(() => {
      vi.advanceTimersByTime(SEEN_MS - 1);
    });
    expect(seen).not.toHaveBeenCalled();
    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(seen).toHaveBeenCalledWith('a');
    // Seen once: no longer observed.
    fire(screen.getByTestId('card-a'), 1);
    act(() => {
      vi.advanceTimersByTime(5 * SEEN_MS);
    });
    expect(seen).toHaveBeenCalledTimes(1);
  });

  it('ignores less than half visible and leaving before a second', () => {
    vi.useFakeTimers();
    const seen = vi.fn();
    render(<Cards onSeen={seen} />);
    const b = screen.getByTestId('card-b');
    fire(b, 0.4);
    act(() => {
      vi.advanceTimersByTime(3 * SEEN_MS);
    });
    fire(b, 0.7);
    act(() => {
      vi.advanceTimersByTime(SEEN_MS / 2);
    });
    fire(b, 0);
    act(() => {
      vi.advanceTimersByTime(3 * SEEN_MS);
    });
    expect(seen).not.toHaveBeenCalled();
  });

  it('pauses while the page is hidden', () => {
    vi.useFakeTimers();
    const seen = vi.fn();
    render(<Cards onSeen={seen} />);
    fire(screen.getByTestId('card-b'), 0.9);
    act(() => {
      vi.advanceTimersByTime(SEEN_MS / 2);
    });
    setVisibility('hidden');
    act(() => {
      vi.advanceTimersByTime(3 * SEEN_MS);
    });
    expect(seen).not.toHaveBeenCalled();
    setVisibility('visible');
    act(() => {
      vi.advanceTimersByTime(SEEN_MS);
    });
    expect(seen).toHaveBeenCalledWith('b');
  });

  it('stops observing on unmount', () => {
    const { unmount } = render(<Cards onSeen={vi.fn()} />);
    unmount();
    expect(FakeIO.all.every((io) => io.observed.size === 0)).toBe(true);
  });
});

describe('seen queue', () => {
  it('sends every 5 s, once per ref, in batches of 50', async () => {
    vi.useFakeTimers();
    const calls = stubAPI((c) => (c.path === '/seen' ? json(204, null) : undefined));
    for (let i = 0; i < BATCH + 3; i++) queueSeen(`p${String(i)}`);
    queueSeen('p0');
    expect(pendingSeen()).toHaveLength(BATCH + 3);
    await vi.advanceTimersByTimeAsync(FLUSH_MS - 1);
    expect(calls).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(1);
    const bodies = seenBodies(calls);
    expect(bodies.map((b) => b.length)).toEqual([BATCH, 3]);
    expect(pendingSeen()).toEqual([]);
    queueSeen('p1'); // already sent from this tab
    expect(pendingSeen()).toEqual([]);
  });

  it('retries a failed batch with the next one', async () => {
    let fail = true;
    const calls = stubAPI((c) => {
      if (c.path !== '/seen') return undefined;
      return fail ? json(500, { error: 'internal' }) : json(204, null);
    });
    queueSeen('x');
    await flushSeen();
    expect(pendingSeen()).toEqual(['x']);
    fail = false;
    queueSeen('y');
    await flushSeen();
    expect(seenBodies(calls)).toEqual([['x'], ['x', 'y']]);
    expect(pendingSeen()).toEqual([]);
  });

  it('flushes with keepalive when the page is hidden', async () => {
    const calls = stubAPI((c) => (c.path === '/seen' ? json(204, null) : undefined));
    const Flusher = slots.global?.[0];
    expect(Flusher).toBeDefined();
    if (!Flusher) return;
    render(<Flusher />);
    queueSeen('z');
    setVisibility('hidden');
    await vi.waitFor(() => {
      expect(seenBodies(calls)).toEqual([['z']]);
    });
    expect(calls[0]?.keepalive).toBe(true);
  });
});

describe('recently seen page', () => {
  function renderPage() {
    const router = createMemoryRouter([{ path: '/seen', element: <SeenPage /> }], { initialEntries: ['/seen'] });
    return render(
      <QueryClientProvider client={makeQueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
  }

  it('lists seen posts page by page and never re-tracks them', async () => {
    const calls = stubAPI((c) => {
      if (c.path === '/seen/recent') {
        return json(200, { items: [postJSON({ ref: 's1', mine: false }), postJSON({ ref: 's2', mine: false })], next: 'c1' });
      }
      if (c.path === '/seen/recent?cursor=c1') return json(200, { items: [postJSON({ ref: 's3', mine: false, body: 'third' })] });
      return undefined;
    });
    const { container } = renderPage();
    expect(await screen.findAllByTestId('seen-item')).toHaveLength(2);
    fireEvent.click(screen.getByTestId('seen-more'));
    expect((await screen.findByText('third')).closest('[data-testid="seen-item"]')?.getAttribute('data-ref')).toBe('s3');
    expect(screen.getAllByTestId('seen-item')).toHaveLength(3);
    expect(screen.queryByTestId('seen-more')).toBeNull();
    expect(container.querySelectorAll('[data-seen-ref]')).toHaveLength(0);
    expect(calls.map((c) => c.path)).toEqual(['/seen/recent', '/seen/recent?cursor=c1']);
  });

  it('says when nothing was seen', async () => {
    stubAPI((c) => (c.path === '/seen/recent' ? json(200, { items: [] }) : undefined));
    renderPage();
    expect((await screen.findByTestId('seen-empty')).textContent).toBe(i18n.t('seen:page.empty'));
  });
});
