// Explorer: the canvas layout (centered post on the axis, others alternating,
// edges by row meaning, "shown above" as a dashed edge to the existing node,
// "+N more"), the canvas mirrored by the outline (selecting a node centers it
// and the outline marks it; rows two above stay put), zoom by buttons and
// keyboard, keyboard panning, the minimap only on desktop and only zoomed out,
// and the feed card's tree thumbnail opening the tree.
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { resetEngageStore } from '../../features/engage/store';
import { i18n } from '../../i18n/setup';
import { integrityTestIds } from '../../integrity/types';
import { postVM } from '../../test/fixtures';
import { asRef, type PostVM, type TreeItemVM, type TreeRowVM, type TreeVM } from '../../vm/types';
import type { TreeActions } from '../types';
import explorer from './index';
import { fit, viewBox, zoomAt } from './canvas/Canvas';
import { excerpt, layoutTree, NODE_W, GAP_X, slotOffset } from './canvas/layout';

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

function actions(): TreeActions {
  return {
    like: vi.fn(),
    reply: vi.fn(),
    openTree: vi.fn(),
    hide: vi.fn(),
    showLess: vi.fn(),
    report: vi.fn(),
    recenter: vi.fn(),
    more: vi.fn(),
    loadEarlier: vi.fn(),
    loadLater: vi.fn(),
  };
}

const item = (ref: string, over: Partial<TreeItemVM> = {}, post: Partial<PostVM> = {}): TreeItemVM => ({
  post: postVM({ ref: asRef(ref), body: { source: ref, nodes: [{ t: 'p', c: [{ t: 'text', v: `Body of ${ref}` }] }] }, ...post }),
  tierDivider: false,
  shownAbove: false,
  alsoRepliesTo: [],
  alsoParents: [],
  ...over,
});

const row = (gen: number, label: TreeRowVM['label'], center: string, items: TreeItemVM[], more = 0): TreeRowVM => ({
  gen,
  label,
  center: asRef(center),
  items,
  more,
});

function tree(): TreeVM {
  return {
    snapshot: 'snap',
    focal: asRef('f'),
    hasEarlier: false,
    hasLater: false,
    rows: [
      row(-2, 'earlier', 'p1', [item('gp')]),
      row(-1, 'replying_to', 'f', [item('p1'), item('p2')]),
      row(0, 'this_post', 'p1', [item('f', {}, { tier: 0 }), item('s1')]),
      row(1, 'replies', 'f', [item('c1'), item('c2'), item('c3')], 4),
      row(2, 'replies_to_replies', 'c1', [item('g1'), item('p2', { shownAbove: true })]),
    ],
  };
}

const first = (r: TreeRowVM) => r.items[0]?.post.ref;

describe('canvas layout', () => {
  it('puts the centered post on the axis and alternates the rest', () => {
    expect([0, 1, 2, 3, 4].map(slotOffset)).toEqual([0, 1, -1, 2, -2]);
    const l = layoutTree(tree(), first);
    const x = (ref: string) => l.byRef.get(asRef(ref))?.x;
    expect([x('c1'), x('c2'), x('c3')]).toEqual([0, NODE_W + GAP_X, -(NODE_W + GAP_X)]);
    expect(l.byRef.get(asRef('f'))?.focal).toBe(true);
    // Choosing another center moves it onto the axis.
    const l2 = layoutTree(tree(), (r) => (r.gen === 1 ? asRef('c3') : first(r)));
    expect(l2.byRef.get(asRef('c3'))?.x).toBe(0);
    expect(l2.byRef.get(asRef('c3'))?.centered).toBe(true);
  });

  it('draws edges by row meaning and a shown-above post once', () => {
    const l = layoutTree(tree(), first);
    const e = l.edges.map((x) => `${x.from}>${x.to}${x.dashed ? '~' : ''}`);
    expect(e).toEqual(
      expect.arrayContaining(['gp>p1', 'p1>f', 'p2>f', 'p1>f', 'p1>s1', 'f>c1', 'f>c2', 'f>c3', 'c1>g1', 'c1>p2~']),
    );
    expect(l.nodes.filter((n) => n.ref === asRef('p2'))).toHaveLength(1);
    expect(l.more).toEqual([{ gen: 1, x: 2 * (NODE_W + GAP_X), y: 3 * 112, count: 4 }]);
  });

  it('takes excerpts from the parsed body', () => {
    expect(excerpt([{ t: 'p', c: [{ t: 'text', v: 'Hello ' }, { t: 'strong', c: [{ t: 'text', v: 'world' }] }] }])).toBe('Hello world');
    expect(excerpt(['x'.repeat(80)])).toHaveLength(48);
    expect(excerpt(undefined)).toBe('');
  });

  it('fits and zooms around a point', () => {
    const v = fit({ x: 0, y: 0, w: 2000, h: 400 }, { w: 500, h: 400 });
    expect(v).toEqual({ cx: 1000, cy: 200, k: 0.25 });
    const z = zoomAt({ cx: 0, cy: 0, k: 1 }, 2, 100, 0);
    expect(z).toEqual({ cx: 50, cy: 0, k: 2 });
    expect(viewBox({ cx: 0, cy: 0, k: 2 }, { w: 400, h: 200 })).toEqual({ x: -100, y: -50, w: 200, h: 100 });
    expect(zoomAt({ cx: 0, cy: 0, k: 1 }, 100, 0, 0).k).toBe(2);
  });
});

function setDesktop(on: boolean) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn((q: string) => ({ matches: on, media: q, addEventListener: vi.fn(), removeEventListener: vi.fn() })),
  );
}

describe('explorer tree layout', () => {
  beforeEach(() => {
    resetEngageStore();
    setDesktop(true);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  function show(tv: TreeVM = tree(), a: TreeActions = actions()) {
    render(
      <MemoryRouter>
        <explorer.TreeLayout tree={tv} actions={a} />
      </MemoryRouter>,
    );
    return a;
  }
  const node = (ref: string) => {
    const el = document.querySelector<SVGGElement>(`[data-testid="explorer-node"][data-ref="${ref}"]`);
    if (!el) throw new Error(`no node ${ref}`);
    return el;
  };
  const outlineItem = (ref: string) => document.querySelector<HTMLElement>(`.tree-outline [data-testid="tree-item"][data-ref="${ref}"]`);

  it('mirrors the canvas with the outline, every integrity element in it', () => {
    show();
    expect(screen.getByTestId('tree-layout').dataset.format).toBe('explorer');
    expect(screen.getAllByTestId('explorer-node')).toHaveLength(9);
    const focalRow = document.querySelector<HTMLElement>('[data-testid="tree-row"][data-gen="0"]');
    if (!focalRow) throw new Error('no focal row');
    for (const tid of integrityTestIds.filter((t) => t !== 'integrity-tier-header')) {
      expect(within(focalRow).getAllByTestId(tid).length).toBeGreaterThan(0);
    }
    expect(screen.getByTestId('explorer-svg').getAttribute('aria-label')).toBe(tr('explorer:canvas.label'));
    expect(screen.getByTestId('explorer-more').textContent).toBe(tr('explorer:canvas.more', { count: 4 }));
  });

  it('selecting a node centers it in both views and the outline marks it', () => {
    const a = show();
    fireEvent.click(node('c3'));
    expect(a.recenter).toHaveBeenCalledWith(1, 'c3');
    expect(node('c3').dataset.centered).toBe('true');
    expect(node('c3').dataset.selected).toBe('true');
    expect(outlineItem('c3')?.dataset.centered).toBe('true');
    expect(outlineItem('c3')?.dataset.selected).toBe('true');
    // The outline's own "show its replies" moves the canvas too.
    const btn = outlineItem('c2')?.querySelector<HTMLElement>('[data-testid="tree-center"]');
    if (!btn) throw new Error('no center button');
    fireEvent.click(btn);
    expect(a.recenter).toHaveBeenLastCalledWith(1, 'c2');
    expect(node('c2').dataset.centered).toBe('true');
    // Rows two or more above only select.
    fireEvent.click(node('gp'));
    expect(a.recenter).toHaveBeenCalledTimes(2);
    expect(node('gp').dataset.selected).toBe('true');
  });

  it('zooms by buttons and keys, pans by keys, and fits', () => {
    show();
    const canvas = screen.getByTestId('explorer-canvas');
    const svg = screen.getByTestId('explorer-svg');
    expect(canvas.dataset.zoom).toBe('1.00');
    fireEvent.click(screen.getByTestId('explorer-zoom-in'));
    expect(canvas.dataset.zoom).toBe('1.25');
    fireEvent.keyDown(svg, { key: '-' });
    fireEvent.keyDown(svg, { key: '-' });
    expect(canvas.dataset.zoom).toBe('0.80');
    expect(screen.getByTestId('explorer-zoom').textContent).toBe(tr('explorer:canvas.zoom', { percent: 80 }));
    const before = svg.getAttribute('viewBox');
    fireEvent.keyDown(svg, { key: 'ArrowRight' });
    expect(svg.getAttribute('viewBox')).not.toBe(before);
    fireEvent.keyDown(svg, { key: '0' });
    expect(Number(canvas.dataset.zoom)).toBeLessThanOrEqual(1);
  });

  it('shows the minimap on desktop once zoomed out, and moves the view from it', () => {
    show();
    expect(screen.queryByTestId('explorer-minimap')).toBeNull();
    fireEvent.click(screen.getByTestId('explorer-zoom-out'));
    const map = screen.getByTestId('explorer-minimap');
    expect(map.getAttribute('aria-hidden')).toBe('true');
    const svg = screen.getByTestId('explorer-svg');
    const before = svg.getAttribute('viewBox');
    vi.spyOn(map, 'getBoundingClientRect').mockReturnValue({ left: 0, top: 0, width: 176, height: 112, right: 176, bottom: 112, x: 0, y: 0, toJSON: () => ({}) });
    act(() => {
      fireEvent.pointerDown(map, { clientX: 2, clientY: 2, pointerId: 1 });
    });
    expect(svg.getAttribute('viewBox')).not.toBe(before);
    fireEvent.click(screen.getByTestId('explorer-zoom-in'));
    expect(screen.queryByTestId('explorer-minimap')).toBeNull();
  });

  it('has no minimap on mobile', () => {
    setDesktop(false);
    show();
    fireEvent.click(screen.getByTestId('explorer-zoom-out'));
    fireEvent.click(screen.getByTestId('explorer-zoom-out'));
    expect(screen.queryByTestId('explorer-minimap')).toBeNull();
  });
});

describe('explorer feed card', () => {
  it('carries a tree thumbnail that opens the tree', () => {
    const a = actions();
    const post = postVM({ replyCount: 7, context: [{ ref: asRef('x'), kind: 'ref', authorHandle: 'bob', excerpt: 'hi' }] });
    render(
      <MemoryRouter>
        <explorer.PostLayout post={post} actions={a} variant="feed" />
        <explorer.PostLayout post={postVM({ ref: asRef('t1') })} actions={a} variant="tree" />
      </MemoryRouter>,
    );
    const thumbs = screen.getAllByTestId('explorer-thumb');
    expect(thumbs).toHaveLength(1);
    expect(thumbs[0]?.getAttribute('aria-label')).toBe(tr('explorer:thumb.label', { parents: 1, replies: 7 }));
    fireEvent.click(thumbs[0] as HTMLElement);
    expect(a.openTree).toHaveBeenCalledWith('p1');
  });
});
