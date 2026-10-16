// Tree feature: view-model mapping (tree folding at 10 lines), the state
// changes of an open tree, and the tree page against a stubbed API — opened
// with the saved preset, re-centering (rows below reload, rows above stay),
// "+N more" pages, earlier / later rows, the reopen on tree_snapshot_expired,
// the uniform "not available", and the reader's format.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/errors';
import type { components } from '../../api/schema.gen';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import { asRef } from '../../vm/types';
import { resetEngageStore } from '../engage/store';
import { me, postJSON } from '../post/testdata';
import { fetchMore, fetchRow, openTree } from './api';
import TreePage from './TreePage';
import { addEarlier, appendMore, isExpired, laterFrom, putRow, toTreeVM, treeErrorKey, treeFold } from './vm';

interface Call {
  method: string;
  path: string;
  query: Record<string, string>;
}

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

function stubAPI(handler: (c: Call) => Response | undefined): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn((req: Request) => {
      const u = new URL(req.url);
      const c = { method: req.method, path: u.pathname.replace(/^\/api/, ''), query: Object.fromEntries(u.searchParams) };
      calls.push(c);
      return Promise.resolve(handler(c) ?? json(404, { error: 'not_found' }));
    }),
  );
  return calls;
}

type S = components['schemas'];

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

const post = (ref: string, over: Record<string, unknown> = {}): S['PostView'] =>
  postJSON({
    ref,
    body: `post ${ref}`,
    mine: false,
    tier: 1,
    author: {
      ref: `u-${ref}`,
      handle: 'bob',
      display_name: 'Bob',
      inviter: { kind: 'user', handle: 'carol' },
      chain: { dist: 1, hidden_middle: 0, first_hop_plus: false },
    },
    ...over,
  }) as S['PostView'];

const item = (ref: string, over: Record<string, unknown> = {}, p: Record<string, unknown> = {}): S['TreeItem'] => ({ post: post(ref, p), ...over });

const rowJSON = (gen: number, label: S['TreeRow']['label'], center: string, items: S['TreeItem'][], more = 0, cursor?: string): S['TreeRow'] => ({
  gen,
  label,
  center,
  items,
  more,
  ...(cursor ? { cursor } : {}),
});

function view(snapshot = 'snap-1'): S['TreeView'] {
  return {
    snapshot,
    focal: 'f',
    has_earlier: true,
    has_later: true,
    rows: [
      rowJSON(-2, 'earlier', 'p1', [item('g1')]),
      rowJSON(-1, 'replying_to', 'f', [item('p1'), item('p2')]),
      rowJSON(0, 'this_post', 'p1', [item('f', {}, { body: 'the focal post' }), item('s1', { tier_divider: true }, { tier: 2 })]),
      rowJSON(1, 'replies', 'f', [item('c1'), item('c2')]),
      rowJSON(2, 'replies_to_replies', 'c1', [item('d1', {}, { reply_count: 2 })]),
    ],
  };
}

const session = () => json(200, { authenticated: true, kind: 'user', csrf: 'c', me });

function treeAPI(extra: (c: Call) => Response | undefined = () => undefined, prefs: Record<string, unknown> = {}): Call[] {
  return stubAPI((c) => {
    const r = extra(c);
    if (r) return r;
    if (c.path === '/session') return session();
    if (c.path === '/me') return json(200, { ...me, settings: { ...me.settings, ui_prefs: prefs } });
    if (c.path === '/tree/f') return json(200, view());
    if (c.path === '/tree/s/snap-1/row') {
      const g = Number(c.query.gen);
      if (g === 2 && c.query.center === 'c2') return json(200, rowJSON(2, 'replies_to_replies', 'c2', [item('e1')]));
      if (g === 3) return json(200, rowJSON(3, 'generations_down', c.query.center ?? '', [item('h1')]));
      if (g === 0) return json(200, rowJSON(0, 'this_post', c.query.center ?? '', [item('f'), item('s9')]));
      if (g === -3) return json(200, rowJSON(-3, 'generations_up', c.query.center ?? '', []));
    }
    return undefined;
  });
}

function renderAt(path = '/p/f') {
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/p/:ref" element={<TreePage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

const rowOf = (gen: number) => {
  const el = document.querySelector<HTMLElement>(`[data-testid="tree-row"][data-gen="${String(gen)}"]`);
  if (!el) throw new Error(`no row ${String(gen)}`);
  return el;
};
const refsOf = (gen: number) => within(rowOf(gen)).getAllByTestId('tree-item').map((e) => e.dataset.ref);
const treeCalls = (calls: Call[]) => calls.filter((c) => c.path.startsWith('/tree'));

beforeEach(() => {
  resetEngageStore();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('tree view-models', () => {
  it('maps the view, rows and items; posts fold past 10 lines in the tree', () => {
    const v = view();
    const long = Array.from({ length: 11 }, (_, i) => `line ${String(i)}`).join('\n');
    v.rows[3] = rowJSON(1, 'replies', 'f', [item('c1', { also_replies_to: [{ ref: 'u-x', handle: 'xena' }], also_parents: ['p9'] }, { body: long })], 3, 'cur');
    const vm = toTreeVM(v);
    expect(vm.snapshot).toBe('snap-1');
    expect(vm.focal).toBe('f');
    expect(vm.hasEarlier && vm.hasLater).toBe(true);
    expect(vm.rows.map((r) => [r.gen, r.label, r.center, r.items.length])).toEqual([
      [-2, 'earlier', 'p1', 1],
      [-1, 'replying_to', 'f', 2],
      [0, 'this_post', 'p1', 2],
      [1, 'replies', 'f', 1],
      [2, 'replies_to_replies', 'c1', 1],
    ]);
    const c1 = vm.rows[3]?.items[0];
    expect(c1?.post.fold).toBe('tree');
    expect(c1?.alsoRepliesTo).toEqual([{ ref: 'u-x', handle: 'xena' }]);
    expect(c1?.alsoParents).toEqual(['p9']);
    expect(vm.rows[3]?.more).toBe(3);
    expect(vm.rows[3]?.cursor).toBe('cur');
    expect(vm.rows[2]?.items[1]?.tierDivider).toBe(true);
    expect(vm.rows[2]?.items[0]?.post.fold).toBe('none');
  });

  it('folds at 10 lines, counting very long lines as several', () => {
    expect(treeFold('')).toBe('none');
    expect(treeFold(Array.from({ length: 10 }, () => 'x').join('\n'))).toBe('none');
    expect(treeFold(Array.from({ length: 11 }, () => 'x').join('\n'))).toBe('tree');
    expect(treeFold('y'.repeat(700))).toBe('none');
    expect(treeFold('y'.repeat(701))).toBe('tree');
  });

  it('puts rows (dropping the rows below a replaced one), appends pages and earlier rows', () => {
    const vm = toTreeVM(view());
    const r1 = toTreeVM({ ...view(), rows: [rowJSON(1, 'replies', 'f', [item('n1')])] }).rows[0];
    if (!r1) throw new Error('no row');
    expect(putRow(vm, r1).rows.map((r) => r.gen)).toEqual([-2, -1, 0, 1]);
    const r0 = { ...r1, gen: 0 };
    expect(putRow(vm, r0).rows.map((r) => r.gen)).toEqual([-2, -1, 0, 1, 2]);
    expect(putRow(vm, { ...r1, items: [] }).rows.map((r) => r.gen)).toEqual([-2, -1, 0]);
    const page = { ...r1, gen: 1, center: asRef('f'), items: r1.items, more: 0 };
    const withMore = appendMore({ ...vm, rows: vm.rows.map((r) => (r.gen === 1 ? { ...r, more: 1, cursor: 'c' } : r)) }, page);
    const merged = withMore.rows.find((r) => r.gen === 1);
    expect(merged?.items.map((i) => i.post.ref)).toEqual(['c1', 'c2', 'n1']);
    expect(merged?.more).toBe(0);
    expect(merged?.cursor).toBeUndefined();
    expect(addEarlier(vm, { ...r1, gen: -3, items: [] }).hasEarlier).toBe(false);
    const up = addEarlier(vm, { ...r1, gen: -3 });
    expect(up.rows[0]?.gen).toBe(-3);
    expect(up.hasEarlier).toBe(false);
    expect(addEarlier(vm, { ...r1, gen: -3, beyond: true }).hasEarlier).toBe(true);
    // A post of the new top row already shown below becomes "shown above" there.
    const below = vm.rows.find((r) => r.gen === 0)?.items[1];
    if (!below) throw new Error('no sibling');
    const dup = addEarlier(vm, { ...r1, gen: -3, items: [below] });
    expect(dup.rows[0]?.items[0]?.shownAbove).toBe(false);
    expect(dup.rows.find((r) => r.gen === 0)?.items[1]?.shownAbove).toBe(true);
    expect(dup.rows.find((r) => r.gen === 0)?.items[0]?.shownAbove).toBe(false);
    expect(laterFrom(vm.rows.at(-1), undefined)).toBe(true);
    expect(laterFrom(vm.rows[0], undefined)).toBe(false);
  });

  it('localizes errors and recognizes an expired snapshot', () => {
    expect(isExpired(new ApiError(409, { error: 'tree_snapshot_expired' }))).toBe(true);
    expect(isExpired(new ApiError(409, { error: 'feed_session_expired' }))).toBe(false);
    expect(treeErrorKey(new ApiError(404, { error: 'not_found' }))).toBe('tree:errors.not_found');
    expect(treeErrorKey(new ApiError(429, { error: 'rate_limited' }))).toBe('errors:rate_limited');
  });

  it('calls the tree endpoints', async () => {
    const calls = treeAPI((c) => (c.path === '/tree/s/snap-1/more' ? json(200, rowJSON(1, 'replies', 'f', [item('c9')])) : undefined));
    expect((await openTree('f')).rows).toHaveLength(5);
    expect((await fetchRow('snap-1', 2, asRef('c2'))).items[0]?.post.ref).toBe('e1');
    expect((await fetchMore('snap-1', 1, asRef('f'), 'cur')).items[0]?.post.ref).toBe('c9');
    expect(calls.map((c) => [c.path, c.query])).toEqual([
      ['/tree/f', {}],
      ['/tree/s/snap-1/row', { gen: '2', center: 'c2' }],
      ['/tree/s/snap-1/more', { gen: '1', center: 'f', cursor: 'cur' }],
    ]);
  });
});

describe('tree page', () => {
  it('opens the tree around the focal post in the reader format', async () => {
    const calls = treeAPI();
    renderAt();
    await screen.findByTestId('tree-layout');
    expect(screen.getByTestId('tree-layout').dataset.format).toBe('cards');
    expect(screen.getAllByTestId('tree-row').map((r) => r.dataset.gen)).toEqual(['-2', '-1', '0', '1', '2']);
    expect(screen.getByText('the focal post')).toBeTruthy();
    expect(treeCalls(calls)).toEqual([{ method: 'GET', path: '/tree/f', query: {} }]);
  });

  it('uses the Compact outline when the reader chose it', async () => {
    treeAPI(undefined, { format: 'compact' });
    renderAt();
    await waitFor(() => {
      expect(screen.getByTestId('tree-layout').dataset.format).toBe('compact');
    });
  });

  it('re-centers a row: the rows below reload, the rows above stay', async () => {
    const calls = treeAPI();
    renderAt();
    await screen.findByTestId('tree-layout');
    fireEvent.click(within(rowOf(1)).getByTestId('tree-next'));
    await waitFor(() => {
      expect(refsOf(2)).toEqual(['e1']);
    });
    expect(refsOf(-2)).toEqual(['g1']);
    expect(treeCalls(calls).slice(1)).toEqual([{ method: 'GET', path: '/tree/s/snap-1/row', query: { gen: '2', center: 'c2' } }]);
    // Another parent: the focal row lists the replies under it.
    fireEvent.click(within(rowOf(-1)).getByTestId('tree-next'));
    await waitFor(() => {
      expect(refsOf(0)).toEqual(['f', 's9']);
    });
    expect(treeCalls(calls).at(-1)?.query).toEqual({ gen: '0', center: 'p2' });
    expect(refsOf(1)).toEqual(['c1', 'c2']);
  });

  it('loads later rows from the centered post and stops when there are no earlier ones', async () => {
    const calls = treeAPI();
    renderAt();
    await screen.findByTestId('tree-layout');
    fireEvent.click(screen.getByTestId('tree-later'));
    await waitFor(() => {
      expect(refsOf(3)).toEqual(['h1']);
    });
    expect(treeCalls(calls).at(-1)?.query).toEqual({ gen: '3', center: 'd1' });
    expect(screen.queryByTestId('tree-later')).toBeNull(); // h1 has no replies
    fireEvent.click(screen.getByTestId('tree-earlier'));
    await waitFor(() => {
      expect(screen.queryByTestId('tree-earlier')).toBeNull();
    });
    expect(treeCalls(calls).at(-1)?.query).toEqual({ gen: '-3', center: 'g1' });
  });

  it('reopens the tree when the snapshot expired', async () => {
    let opens = 0;
    const calls = treeAPI((c) => {
      if (c.path === '/tree/f') {
        opens++;
        return json(200, view(opens === 1 ? 'snap-1' : 'snap-2'));
      }
      if (c.path === '/tree/s/snap-1/row') return json(409, { error: 'tree_snapshot_expired' });
      return undefined;
    });
    renderAt();
    await screen.findByTestId('tree-layout');
    fireEvent.click(within(rowOf(1)).getByTestId('tree-next'));
    await waitFor(() => {
      expect(screen.getByTestId('tree-page').dataset.opened).toBe('1');
    });
    await waitFor(() => {
      expect(treeCalls(calls).filter((c) => c.path === '/tree/f')).toHaveLength(2);
    });
    expect(screen.queryByTestId('tree-action-error')).toBeNull();
  });

  it('shows the uniform "not available" for a post it cannot open', async () => {
    treeAPI();
    renderAt('/p/nope');
    const err = await screen.findByTestId('tree-error');
    expect(err.textContent).toContain(tr('tree:errors.not_found'));
    expect(screen.queryByTestId('tree-layout')).toBeNull();
  });
});
