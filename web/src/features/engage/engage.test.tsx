// Engage feature: like-summary mapping, the structural cache patcher, the like
// summary text, and usePostActions on the likes page against a stubbed API.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { createMemoryRouter, MemoryRouter, RouterProvider, useLocation } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import { postVM } from '../../test/fixtures';
import { asRef, type LikesVM } from '../../vm/types';
import { postJSON } from '../post/testdata';
import { pendingSeen, resetSeenQueue } from '../seen/tracker';
import { LikeSummary } from './EngageBar';
import { LikesModal } from './LikesModal';
import HiddenPage from './HiddenPage';
import LikesPage from './LikesPage';
import { resetEngageStore } from './store';
import { applyLikes, usePostActions } from './usePostActions';
import { optimistic, patchLikes, patchSaved, toLikesVM } from './vm';

interface Call {
  method: string;
  path: string;
  body: unknown;
}

function json(status: number, body: unknown): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function stubAPI(handler: (c: Call) => Response | Promise<Response> | undefined): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const text = await req.clone().text();
      const c = { method: req.method, path: new URL(req.url).pathname.replace(/^\/api/, ''), body: text ? (JSON.parse(text) as unknown) : undefined };
      calls.push(c);
      return (await handler(c)) ?? json(404, { error: 'not_found' });
    }),
  );
  return calls;
}

function renderAt(path: string) {
  const router = createMemoryRouter(
    [
      { path: '/p/:ref/likes', element: <LikesPage /> },
      { path: '/p/:ref', element: <p>tree page</p> },
      { path: '/people/:ref', element: <p>person page</p> },
    ],
    { initialEntries: [path] },
  );
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

const likes = (over: Partial<LikesVM> = {}): LikesVM => ({ inNetwork: 0, names: [], others: 0, byMe: false, ...over });

beforeEach(() => {
  resetEngageStore();
  resetSeenQueue();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('engage view-models', () => {
  it('maps a LikeSummary', () => {
    expect(toLikesVM({ in_network: 4, names: [{ ref: 'u2', handle: 'bob' }], others: 3, by_me: false })).toEqual({
      inNetwork: 4,
      names: [{ ref: asRef('u2'), handle: 'bob' }],
      others: 3,
      byMe: false,
    });
    expect(toLikesVM({ in_network: 0, others: 0, by_me: false }).names).toEqual([]);
  });

  it('counts the viewer in their own network while a like is in flight', () => {
    expect(optimistic(likes({ inNetwork: 2 }), true)).toEqual(likes({ inNetwork: 3, byMe: true }));
    expect(optimistic(likes({ inNetwork: 3, byMe: true }), false)).toEqual(likes({ inNetwork: 2 }));
    const same = likes({ inNetwork: 1, byMe: true });
    expect(optimistic(same, true)).toBe(same);
    expect(optimistic(likes(), false).inNetwork).toBe(0);
  });

  it('patches every cached post with the ref and shares the rest', () => {
    const a = postVM({ ref: asRef('a') });
    const b = postVM({ ref: asRef('b') });
    const page = { session: 's', items: [{ type: 'post', post: a }, { type: 'thread', parts: [b, a] }], other: { n: 1 } };
    const next = patchLikes(page, 'a', likes({ inNetwork: 9 }));
    expect(next).not.toBe(page);
    expect(next.items[0]).toEqual({ type: 'post', post: { ...a, likes: likes({ inNetwork: 9 }) } });
    const thread = next.items[1] as { parts: (typeof a)[] };
    expect(thread.parts[0]).toBe(b);
    expect(thread.parts[1]?.likes.inNetwork).toBe(9);
    expect(thread.parts[1]?.createdAt).toBeInstanceOf(Date);
    expect(next.other).toBe(page.other);
    expect(patchLikes(page, 'zzz', likes())).toBe(page);
    expect(patchLikes<unknown>(null, 'a', likes())).toBeNull();
  });

  it('applies a summary to every query that caches the post', () => {
    const qc = new QueryClient();
    qc.setQueryData(['post', 'a'], postVM({ ref: asRef('a') }));
    qc.setQueryData(['feed', 's1'], { pages: [{ items: [{ type: 'post', post: postVM({ ref: asRef('a') }) }] }] });
    qc.setQueryData(['other'], { n: 1 });
    applyLikes(qc, 'a', likes({ inNetwork: 5, byMe: true }));
    expect(qc.getQueryData<ReturnType<typeof postVM>>(['post', 'a'])?.likes.inNetwork).toBe(5);
    expect(JSON.stringify(qc.getQueryData(['feed', 's1']))).toContain('"inNetwork":5');
    expect(qc.getQueryData(['likes', 'a'])).toEqual(likes({ inNetwork: 5, byMe: true }));
    expect(qc.getQueryData(['other'])).toEqual({ n: 1 });
  });
});

describe('like summary', () => {
  function show(l: LikesVM) {
    return render(
      <MemoryRouter>
        <LikeSummary likes={l} />
      </MemoryRouter>,
    );
  }

  it('says when nobody in the network liked it', () => {
    show(likes());
    expect(screen.getByTestId('engage-likes').textContent).toBe(tr('engage:likes.none'));
  });

  it('counts in-network likes without names', () => {
    show(likes({ inNetwork: 14, others: 14 }));
    expect(screen.getByTestId('engage-likes').textContent).toBe(tr('engage:likes.inNetwork', { count: 14 }));
  });

  it('names friends, the viewer and the others', () => {
    show(likes({ inNetwork: 5, byMe: true, others: 2, names: [{ ref: asRef('u2'), handle: 'bob' }, { ref: asRef('u3'), handle: 'carol' }] }));
    const text = screen.getByTestId('engage-likes').textContent;
    expect(text).toContain(tr('engage:likes.likedBy'));
    expect(text).toContain(tr('engage:likes.you'));
    expect(text).toContain('@bob');
    expect(text).toContain(tr('engage:likes.others', { count: 2 }));
    const links = screen.getAllByTestId('engage-liker');
    expect(links.map((l) => l.getAttribute('href'))).toEqual(['/people/u2', '/people/u3']);
  });
});

describe('likes modal', () => {
  function Where() {
    const loc = useLocation();
    return <p data-testid="where">{loc.pathname + loc.search}</p>;
  }

  function renderShell(path: string, l: LikesVM) {
    const router = createMemoryRouter(
      [
        {
          path: '/',
          element: (
            <>
              <LikeSummary likes={l} post={asRef('p1')} />
              <Where />
              <LikesModal />
            </>
          ),
        },
        { path: '/people/:ref', element: <p>person page</p> },
      ],
      { initialEntries: [path] },
    );
    render(
      <QueryClientProvider client={makeQueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
    return router;
  }

  it('opens from the summary, lists the named likers, and the back button closes it', async () => {
    const summary = likes({ inNetwork: 4, byMe: true, others: 2, names: [{ ref: asRef('u2'), handle: 'bob' }] });
    stubAPI((c) => {
      if (c.method === 'GET' && c.path === '/posts/p1/likes') return json(200, { in_network: 4, names: [{ ref: 'u2', handle: 'bob' }], others: 2, by_me: true });
      return undefined;
    });
    const router = renderShell('/', summary);
    expect(screen.queryByTestId('likes-modal')).toBeNull();
    fireEvent.click(screen.getByTestId('engage-likes-open'));
    expect(screen.getByTestId('where').textContent).toBe('/?likes=p1');
    const dialog = await screen.findByTestId('likes-modal');
    expect(dialog.tagName).toBe('DIALOG');
    expect(dialog.getAttribute('aria-labelledby')).toBeTruthy();
    expect((await screen.findByTestId('likes-modal-count')).dataset.count).toBe('4');
    expect(screen.getAllByTestId('likes-modal-liker').map((a) => a.getAttribute('href'))).toEqual(['/people/u2']);
    expect(screen.getByTestId('likes-modal-list').textContent).toContain(tr('engage:modal.others', { count: 2 }));
    expect(screen.getByTestId('likes-modal-like').getAttribute('aria-pressed')).toBe('true');
    await act(async () => {
      await router.navigate(-1);
    });
    expect(screen.queryByTestId('likes-modal')).toBeNull();
    expect(screen.getByTestId('where').textContent).toBe('/');
  });

  it('opens from a link and closes in place with Escape or the close button', async () => {
    stubAPI((c) => {
      if (c.method === 'GET' && c.path === '/posts/p1/likes') return json(200, { in_network: 3, others: 3, by_me: false });
      return undefined;
    });
    renderShell('/?likes=p1', likes({ inNetwork: 3, others: 3 }));
    const dialog = await screen.findByTestId('likes-modal');
    fireEvent.keyDown(dialog, { key: 'Escape' });
    await waitFor(() => {
      expect(screen.queryByTestId('likes-modal')).toBeNull();
    });
    expect(screen.getByTestId('where').textContent).toBe('/');
    fireEvent.click(screen.getByTestId('engage-likes-open'));
    await screen.findByTestId('likes-modal');
    fireEvent.click(screen.getByTestId('modal-close'));
    await waitFor(() => {
      expect(screen.queryByTestId('likes-modal')).toBeNull();
    });
  });

  it('says so when the post is not there', async () => {
    stubAPI(() => undefined);
    renderShell('/?likes=nope', likes());
    expect((await screen.findByTestId('likes-modal-error')).textContent).toBe(tr('engage:modal.missing'));
  });
});

describe('likes page and post actions', () => {
  it('likes optimistically, then shows the server summary; unlikes', async () => {
    let release: (() => void) | undefined;
    const calls = stubAPI((c) => {
      if (c.method === 'GET' && c.path === '/posts/p1') return json(200, postJSON({ mine: false, likes: { in_network: 2, others: 2, by_me: false } }));
      if (c.method === 'PUT' && c.path === '/posts/p1/like') {
        return new Promise<Response>((resolve) => {
          release = () => {
            resolve(json(200, { likes: { in_network: 3, names: [{ ref: 'u2', handle: 'bob' }], others: 1, by_me: true } }));
          };
        });
      }
      if (c.method === 'DELETE' && c.path === '/posts/p1/like') return json(200, { likes: { in_network: 2, others: 2, by_me: false } });
      return undefined;
    });
    renderAt('/p/p1/likes');
    const btn = await screen.findByTestId('engage-like');
    expect(btn.getAttribute('aria-pressed')).toBe('false');
    fireEvent.click(btn);
    // Optimistic: pressed and counted before the server answers.
    await waitFor(() => {
      expect(screen.getByTestId('engage-like').getAttribute('aria-pressed')).toBe('true');
    });
    expect(screen.getByTestId('engage-likes').dataset.count).toBe('3');
    await waitFor(() => {
      expect(release).toBeDefined();
    });
    act(() => {
      release?.();
    });
    expect(await screen.findByTestId('engage-liker')).toBeTruthy();
    fireEvent.click(screen.getByTestId('engage-like'));
    await waitFor(() => {
      expect(screen.getByTestId('engage-like').getAttribute('aria-pressed')).toBe('false');
    });
    await waitFor(() => {
      expect(calls.filter((c) => c.path === '/posts/p1/like').map((c) => c.method)).toEqual(['PUT', 'DELETE']);
    });
  });

  it('rolls a failed like back and says why', async () => {
    stubAPI((c) => {
      if (c.method === 'GET' && c.path === '/posts/p1') return json(200, postJSON({ mine: false }));
      if (c.path === '/posts/p1/like') return json(429, { error: 'rate_limited', retry_after_s: 30 });
      return undefined;
    });
    renderAt('/p/p1/likes');
    fireEvent.click(await screen.findByTestId('engage-like'));
    expect(await screen.findByTestId('engage-error')).toBeTruthy();
    expect(screen.getByTestId('engage-like').getAttribute('aria-pressed')).toBe('false');
    expect(screen.getByTestId('engage-likes').dataset.count).toBe('0');
  });

  it('hides and shows less, rolling back on failure', async () => {
    const calls = stubAPI((c) => {
      if (c.method === 'GET' && c.path === '/posts/p1') return json(200, postJSON({ mine: false }));
      if (c.method === 'GET' && c.path === '/posts/p2') return json(200, postJSON({ ref: 'p2', mine: false }));
      if (c.path === '/posts/p1/show-less') return json(204, null);
      if (c.path === '/posts/p2/hide') return json(404, { error: 'not_found' });
      return undefined;
    });
    renderAt('/p/p1/likes');
    fireEvent.click(await screen.findByTestId('engage-show-less'));
    const done = await screen.findByTestId('engage-hidden');
    expect(done.dataset.kind).toBe('show_less');
    expect(done.querySelector('[role="status"]')?.textContent).toBe(tr('engage:showLess.done', { handle: 'alice' }));
    expect(calls.some((c) => c.method === 'POST' && c.path === '/posts/p1/show-less')).toBe(true);

    renderAt('/p/p2/likes');
    const hides = await screen.findAllByTestId('engage-hide');
    fireEvent.click(hides[hides.length - 1] as HTMLElement);
    expect(await screen.findByTestId('engage-error')).toBeTruthy();
    expect(screen.queryAllByTestId('engage-hidden')).toHaveLength(1); // only p1 stays hidden
  });

  it('undoes a hide from the notice', async () => {
    const calls = stubAPI((c) => {
      if (c.method === 'GET' && c.path === '/posts/p1') return json(200, postJSON({ mine: false }));
      if (c.path === '/posts/p1/hide') return json(204, null);
      return undefined;
    });
    renderAt('/p/p1/likes');
    fireEvent.click(await screen.findByTestId('engage-hide'));
    fireEvent.click(await screen.findByTestId('engage-unhide'));
    expect(await screen.findByTestId('engage-hide')).toBeTruthy();
    expect(screen.queryByTestId('engage-hidden')).toBeNull();
    await waitFor(() => {
      expect(calls.some((c) => c.method === 'DELETE' && c.path === '/posts/p1/hide')).toBe(true);
    });
  });

  it('lists hidden posts and show-less choices, each with an undo', async () => {
    const calls = stubAPI((c) => {
      if (c.method === 'GET' && c.path === '/show-less')
        return json(200, { items: [{ ref: 'u9', handle: 'carol', display_name: 'Carol', count: 2 }] });
      if (c.method === 'GET' && c.path === '/hidden') return json(200, { items: [postJSON({ mine: false })] });
      if (c.method === 'DELETE') return json(204, null);
      return undefined;
    });
    const router = createMemoryRouter([{ path: '/settings/hidden', element: <HiddenPage /> }], {
      initialEntries: ['/settings/hidden'],
    });
    render(
      <QueryClientProvider client={makeQueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
    const person = await screen.findByTestId('hidden-less-item');
    expect(person.textContent).toContain(tr('engage:hidden.less.count', { count: 2 }));
    fireEvent.click(screen.getByTestId('hidden-less-reset'));
    expect(await screen.findByText(tr('engage:hidden.less.done'))).toBeTruthy();
    fireEvent.click(await screen.findByTestId('hidden-unhide'));
    expect(await screen.findByText(tr('engage:hidden.posts.done'))).toBeTruthy();
    expect(calls.filter((c) => c.method === 'DELETE').map((c) => c.path)).toEqual(['/show-less/u9', '/posts/p1/hide']);
  });

  it('saves privately and optimistically, rolling back on failure', async () => {
    let fail = false;
    const calls = stubAPI((c) => {
      if (c.method === 'GET' && c.path === '/posts/p1') return json(200, postJSON({ mine: false, likes: { in_network: 2, others: 2, by_me: false } }));
      if (c.path === '/posts/p1/save') return fail ? json(429, { error: 'rate_limited', retry_after_s: 30 }) : json(204, null);
      return undefined;
    });
    renderAt('/p/p1/likes');
    const btn = await screen.findByTestId('engage-save');
    expect(btn.getAttribute('aria-pressed')).toBe('false');
    expect(btn.getAttribute('aria-label')).toBe(tr('engage:save.action'));
    expect(btn.getAttribute('aria-describedby')).toBeTruthy();
    fireEvent.click(btn);
    await waitFor(() => {
      expect(screen.getByTestId('engage-save').getAttribute('aria-pressed')).toBe('true');
    });
    // A save never moves the like summary.
    expect(screen.getByTestId('engage-likes').dataset.count).toBe('2');
    await waitFor(() => {
      expect(calls.filter((c) => c.path === '/posts/p1/save').map((c) => c.method)).toEqual(['PUT']);
    });
    fail = true;
    fireEvent.click(screen.getByTestId('engage-save'));
    expect(await screen.findByTestId('engage-error')).toBeTruthy();
    expect(screen.getByTestId('engage-save').getAttribute('aria-pressed')).toBe('true');
    expect(calls.filter((c) => c.path === '/posts/p1/save').map((c) => c.method)).toEqual(['PUT', 'DELETE']);
    // The message is formatted (it once showed its raw {seconds, plural, …} template).
    expect(screen.getByTestId('engage-error').textContent).toBe(tr('errors:rate_limited', { seconds: 30 }));
  });

  it('takes a quick save and unsave without an error', async () => {
    const calls = stubAPI((c) => {
      if (c.method === 'GET' && c.path === '/posts/p1') return json(200, postJSON({ mine: false }));
      if (c.path === '/posts/p1/like') return json(200, { likes: { in_network: c.method === 'PUT' ? 1 : 0, others: 0, by_me: c.method === 'PUT' } });
      return json(204, null);
    });
    renderAt('/p/p1/likes');
    const pressed = (id: string, on: boolean) =>
      waitFor(() => {
        expect(screen.getByTestId(id).getAttribute('aria-pressed')).toBe(String(on));
      });
    fireEvent.click(await screen.findByTestId('engage-save'));
    await pressed('engage-save', true);
    fireEvent.click(screen.getByTestId('engage-save'));
    await pressed('engage-save', false);
    fireEvent.click(screen.getByTestId('engage-like'));
    await pressed('engage-like', true);
    fireEvent.click(screen.getByTestId('engage-like'));
    await pressed('engage-like', false);
    // Each toggle went out and came back; nothing was refused or rolled back.
    const sent = (path: string) => calls.filter((c) => c.path === path).map((c) => c.method);
    await waitFor(() => {
      expect(sent('/posts/p1/save')).toEqual(['PUT', 'DELETE']);
      expect(sent('/posts/p1/like')).toEqual(['PUT', 'DELETE']);
    });
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.getByTestId('engage-save').getAttribute('aria-pressed')).toBe('false');
    expect(screen.getByTestId('engage-like').getAttribute('aria-pressed')).toBe('false');
    expect(screen.queryByTestId('engage-error')).toBeNull();
  });

  it('patches saved_by_me in every cached post', () => {
    const p = postVM({ ref: asRef('p1') });
    const other = postVM({ ref: asRef('p2') });
    const data = { pages: [{ items: [{ post: p }, { post: other }] }] };
    const next = patchSaved(data, 'p1', true);
    expect(next.pages[0]?.items[0]?.post.savedByMe).toBe(true);
    expect(next.pages[0]?.items[1]).toBe(data.pages[0]?.items[1]);
    expect(patchSaved(next, 'p1', true)).toBe(next);
  });

  it('offers no hide or show-less on your own post', async () => {
    stubAPI((c) => (c.method === 'GET' && c.path === '/posts/p1' ? json(200, postJSON()) : undefined));
    renderAt('/p/p1/likes');
    await screen.findByTestId('engage-like');
    expect(screen.queryByTestId('engage-hide')).toBeNull();
    expect(screen.queryByTestId('engage-show-less')).toBeNull();
  });

  it('marks only the card itself for the seen tracker', async () => {
    stubAPI((c) =>
      c.method === 'GET' && c.path === '/posts/p1'
        ? json(200, postJSON({ mine: false, context: [{ ref: 'p0', kind: 'ref', author_handle: 'bob', excerpt: 'parent' }] }))
        : undefined,
    );
    const { container } = renderAt('/p/p1/likes');
    await screen.findByTestId('engage-card');
    const marked = container.querySelectorAll('[data-seen-ref]');
    expect([...marked].map((e) => e.getAttribute('data-seen-ref'))).toEqual(['p1']);
    expect(screen.getByTestId('post-context').closest('[data-seen-ref]')?.getAttribute('data-seen-ref')).toBe('p1');
    expect(screen.getByTestId('post-context').hasAttribute('data-seen-ref')).toBe(false);
  });

  it('is not found for refs the server does not know', async () => {
    stubAPI(() => undefined);
    renderAt('/p/zz/likes');
    expect(await screen.findByText(tr('common:notFound.title'))).toBeTruthy();
  });

  it('navigates for reply and tree, opens the report modal, and queues seen posts', () => {
    let actions: ReturnType<typeof usePostActions> | undefined;
    function Probe() {
      actions = usePostActions();
      const loc = useLocation();
      return <output data-testid="loc">{loc.pathname + loc.search}</output>;
    }
    render(
      <QueryClientProvider client={makeQueryClient()}>
        <MemoryRouter>
          <Probe />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    act(() => {
      actions?.markSeen?.(asRef('p9'));
    });
    expect(pendingSeen()).toEqual(['p9']);
    const at = () => screen.getByTestId('loc').textContent;
    act(() => {
      actions?.reply(asRef('p9'));
    });
    expect(at()).toBe('/compose?parent=p9&kind=ref');
    act(() => {
      actions?.openTree(asRef('p9'));
    });
    expect(at()).toBe('/p/p9');
    act(() => {
      actions?.report({ kind: 'post', ref: asRef('p9') });
    });
    expect(at()).toBe('/p/p9?report=post%3Ap9');
  });
});
