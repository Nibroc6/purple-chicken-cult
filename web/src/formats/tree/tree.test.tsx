// The reply-tree canvas every format shares: the layout (first post of each
// generation on the axis, the rest alternating, edges by row meaning, the
// focal post's other parents as "also" edges, "shown above" as a dashed edge
// to the existing node, "+N more"), family-tree elbow routing, the focused post
// in full with every integrity element in each format, clicking or Enter on a
// post bringing it into focus, "+N more" loading the rest, earlier / later at
// the edges, zoom by buttons, keys and pinch, keyboard panning, a drag that is
// not a click, and the minimap only on desktop and only zoomed out.
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { resetEngageStore } from '../../features/engage/store';
import { i18n } from '../../i18n/setup';
import { integrityTestIds } from '../../integrity/types';
import { postVM } from '../../test/fixtures';
import { asRef, type PostVM, type TreeItemVM, type TreeRowVM, type TreeVM } from '../../vm/types';
import cards from '../cards';
import compact from '../compact';
import explorer from '../explorer';
import type { FormatModule, TreeActions } from '../types';
import { fit, viewBox, zoomAt } from './canvas/Canvas';
import { excerpt, GAP_X, layoutTree, lines, NODE_H, NODE_W, ROW_H, route, slotOffset } from './canvas/layout';

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

function actions(): TreeActions {
  return {
    like: vi.fn(),
    reply: vi.fn(),
    openTree: vi.fn(),
    hide: vi.fn(),
    showLess: vi.fn(),
    report: vi.fn(),
    more: vi.fn(),
    loadEarlier: vi.fn(),
    loadLater: vi.fn(),
  };
}

const item = (ref: string, over: Partial<TreeItemVM> = {}, post: Partial<PostVM> = {}): TreeItemVM => ({
  post: postVM({
    ref: asRef(ref),
    body: {
      source: ref,
      nodes: [{ t: 'p', c: [{ t: 'text', v: `Body of ${ref}` }] }],
    },
    ...post,
  }),
  tierDivider: false,
  shownAbove: false,
  alsoRepliesTo: [],
  alsoParents: [],
  ...over,
});

const row = (gen: number, label: TreeRowVM['label'], center: string, items: TreeItemVM[], more = 0, cursor?: string): TreeRowVM => ({
  gen,
  label,
  center: asRef(center),
  items,
  more,
  ...(cursor ? { cursor } : {}),
});

function tree(over: Partial<TreeVM> = {}): TreeVM {
  return {
    snapshot: 'snap',
    focal: asRef('f'),
    hasEarlier: false,
    hasLater: false,
    rows: [
      row(-2, 'earlier', 'p1', [item('gp')]),
      row(-1, 'replying_to', 'f', [item('p1'), item('p2')]),
      row(0, 'this_post', 'p1', [
        item(
          'f',
          {},
          {
            tier: 0,
            body: {
              source: 'the focal post',
              nodes: [{ t: 'p', c: [{ t: 'text', v: 'the focal post' }] }],
            },
          },
        ),
        item('s1'),
      ]),
      row(1, 'replies', 'f', [item('c1', {}, { replyCount: 2 }), item('c2'), item('c3')], 4, 'cur-1'),
      row(2, 'replies_to_replies', 'c1', [item('g1'), item('p2', { shownAbove: true })]),
    ],
    ...over,
  };
}

const first = () => undefined;

describe('canvas layout', () => {
  it('puts the first post of each generation on the axis and alternates the rest', () => {
    expect([0, 1, 2, 3, 4].map(slotOffset)).toEqual([0, 1, -1, 2, -2]);
    const l = layoutTree(tree(), first);
    const x = (ref: string) => l.byRef.get(asRef(ref))?.x;
    expect([x('c1'), x('c2'), x('c3')]).toEqual([0, NODE_W + GAP_X, -(NODE_W + GAP_X)]);
    expect(l.byRef.get(asRef('f'))?.focal).toBe(true);
    expect(l.byRef.get(asRef('c1'))?.replies).toBe(2);
    // Another center moves it onto the axis.
    const l2 = layoutTree(tree(), (r) => (r.gen === 1 ? asRef('c3') : undefined));
    expect(l2.byRef.get(asRef('c3'))?.x).toBe(0);
    expect(l2.byRef.get(asRef('c3'))?.centered).toBe(true);
  });

  it('draws edges by row meaning: other parents as "also", a shown-above post once', () => {
    const l = layoutTree(tree(), first);
    const e = l.edges.map((x) => `${x.from}>${x.to}:${x.kind}`);
    expect(e).toEqual(
      expect.arrayContaining([
        'gp>p1:reply',
        'p1>f:reply',
        'p2>f:also',
        'p1>s1:reply',
        'f>c1:reply',
        'f>c2:reply',
        'f>c3:reply',
        'c1>g1:reply',
        'c1>p2:above',
      ]),
    );
    expect(l.edges).toHaveLength(9);
    expect(l.nodes.filter((n) => n.ref === asRef('p2'))).toHaveLength(1);
    expect(l.more).toEqual([
      {
        gen: 1,
        center: 'f',
        cursor: 'cur-1',
        x: 2 * (NODE_W + GAP_X),
        y: 3 * ROW_H,
        count: 4,
      },
    ]);
  });

  it('puts a reply to several posts one row below its lowest parent, joined to every parent', () => {
    // c2 replies to the focal post and to c1; g2 replies to c2 (and so moves down with it).
    const tv = tree({
      rows: [
        row(0, 'this_post', 'f', [item('f')]),
        row(1, 'replies', 'f', [item('c1'), item('c2', { alsoParents: [asRef('c1')] }), item('c3')]),
        row(2, 'replies_to_replies', 'c1', [item('g1'), item('c2', { shownAbove: true })]),
        row(3, 'replies_to_replies', 'c2', [item('g2')]),
      ],
    });
    const l = layoutTree(tv, (r) => (r.gen === 2 ? asRef('c2') : undefined));
    const at = (ref: string) => l.byRef.get(asRef(ref));
    expect(at('c1')?.y).toBe(ROW_H);
    expect(at('c2')?.y).toBe(2 * ROW_H);
    expect(at('g1')?.y).toBe(2 * ROW_H);
    expect(at('c2')?.x).not.toBe(at('g1')?.x);
    expect(at('g2')?.y).toBe(3 * ROW_H);
    // c3 closes the gap c2 left in its row.
    expect(at('c3')?.x).toBe(NODE_W + GAP_X);
    const e = l.edges.map((x) => `${x.from}>${x.to}:${x.kind}`);
    expect(e).toEqual(expect.arrayContaining(['f>c2:reply', 'c1>c2:also', 'c1>g1:reply']));
    expect(e.filter((x) => x.endsWith('>c2:above'))).toHaveLength(0);
    expect(l.nodes.filter((n) => n.ref === asRef('c2'))).toHaveLength(1);
  });

  it('routes a line across several rows down a lane between the cards', () => {
    const d = route({ x: 0, y: 0 }, { x: 0, y: 2 * ROW_H }, 'also');
    expect(d).toContain(`L ${String((NODE_W + GAP_X) / 2)}`);
    expect(d.endsWith(`L 0 ${String(2 * ROW_H - NODE_H / 2)}`)).toBe(true);
  });

  it('routes edges as family-tree elbows', () => {
    const a = { x: 0, y: 0 };
    expect(route(a, { x: 0, y: ROW_H }, 'reply')).toBe(`M 0 ${String(NODE_H / 2)} V ${String(ROW_H - NODE_H / 2)}`);
    const mid = NODE_H / 2 + (ROW_H - NODE_H) / 2;
    const d = route(a, { x: 240, y: ROW_H }, 'reply');
    expect(d).toContain(`H ${String(240 - 10)}`);
    expect(d).toContain(`Q 0 ${String(mid)}`);
    expect(d.endsWith(`V ${String(ROW_H - NODE_H / 2)}`)).toBe(true);
    // "also" edges cross higher up, apart from the siblings' line.
    expect(route(a, { x: -240, y: ROW_H }, 'also')).not.toContain(`Q 0 ${String(mid)}`);
  });

  it('loops a shown-above edge under its row, or up to a post higher up', () => {
    const below = NODE_H / 2;
    expect(route({ x: 0, y: 0 }, { x: 240, y: 0 }, 'above')).toMatch(new RegExp(`^M 0 ${String(below)} .* H 230 .* V ${String(below)}$`));
    expect(route({ x: 0, y: ROW_H }, { x: 240, y: 0 }, 'above')).toMatch(new RegExp(`^M 0 ${String(ROW_H - below)} .* V ${String(below)}$`));
  });

  it('wraps an excerpt into two lines between words', () => {
    expect(lines('short')).toEqual(['short']);
    expect(lines('Long reply line 1 Long reply line 2 Long reply line 3', 20)).toEqual(['Long reply line 1', 'Long reply line 2…']);
    expect(lines('x'.repeat(50), 20)).toEqual(['x'.repeat(20), `${'x'.repeat(19)}…`]);
    expect(lines('')).toEqual([]);
  });

  it('takes excerpts from the parsed body', () => {
    expect(
      excerpt([
        {
          t: 'p',
          c: [
            { t: 'text', v: 'Hello ' },
            { t: 'strong', c: [{ t: 'text', v: 'world' }] },
          ],
        },
      ]),
    ).toBe('Hello world');
    expect(excerpt(['x'.repeat(80)])).toHaveLength(56);
    expect(excerpt(undefined)).toBe('');
  });

  it('fits and zooms around a point', () => {
    const v = fit({ x: 0, y: 0, w: 2000, h: 400 }, { w: 500, h: 400 });
    expect(v).toEqual({ cx: 1000, cy: 200, k: 0.25 });
    const z = zoomAt({ cx: 0, cy: 0, k: 1 }, 2, 100, 0);
    expect(z).toEqual({ cx: 50, cy: 0, k: 2 });
    expect(viewBox({ cx: 0, cy: 0, k: 2 }, { w: 400, h: 200 })).toEqual({
      x: -100,
      y: -50,
      w: 200,
      h: 100,
    });
    expect(zoomAt({ cx: 0, cy: 0, k: 1 }, 100, 0, 0).k).toBe(2);
  });
});

function setDesktop(on: boolean) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn((q: string) => ({
      matches: on,
      media: q,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  );
}

describe('connected tree', () => {
  beforeEach(() => {
    resetEngageStore();
    setDesktop(true);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  function show(tv: TreeVM = tree(), a: TreeActions = actions(), format: FormatModule = cards) {
    render(
      <MemoryRouter>
        <format.TreeLayout tree={tv} actions={a} />
      </MemoryRouter>,
    );
    return a;
  }
  const node = (ref: string) => {
    const el = document.querySelector<SVGGElement>(`[data-testid="explorer-node"][data-ref="${ref}"]`);
    if (!el) throw new Error(`no node ${ref}`);
    return el;
  };

  it.each([cards, compact, explorer])('is one canvas in $id, the focused post in full with every integrity element', (format) => {
    show(tree(), actions(), format);
    expect(screen.getByTestId('tree-layout').dataset.format).toBe(format.id);
    expect(screen.queryByTestId('tree-row')).toBeNull();
    expect(screen.getAllByTestId('explorer-node')).toHaveLength(9);
    expect(node('f').dataset.focal).toBe('true');
    expect(node('f').getAttribute('aria-current')).toBe('true');
    const focus = screen.getByTestId('tree-focus');
    expect(focus.dataset.ref).toBe('f');
    expect(within(focus).getByText('the focal post')).toBeTruthy();
    for (const tid of integrityTestIds.filter((t) => t !== 'integrity-tier-header')) {
      expect(within(focus).getAllByTestId(tid).length).toBeGreaterThan(0);
    }
    expect(screen.getByTestId('explorer-svg').getAttribute('aria-label')).toBe(tr('explorer:canvas.label'));
    expect(document.querySelectorAll('.explorer-edge')).toHaveLength(9);
  });

  it('brings a post into focus by click or Enter; the focal post only recenters the view', () => {
    const a = show();
    fireEvent.click(node('c3'));
    expect(a.openTree).toHaveBeenCalledWith('c3');
    fireEvent.keyDown(node('gp'), { key: 'Enter' });
    expect(a.openTree).toHaveBeenLastCalledWith('gp');
    expect(node('gp').getAttribute('aria-label')).toBe(tr('explorer:canvas.open', { handle: 'alice', text: 'Body of gp' }));
    fireEvent.click(node('f'));
    expect(a.openTree).toHaveBeenCalledTimes(2);
  });

  it('floats the controls and the focused post over the canvas; the focus card folds away', () => {
    show();
    expect(document.querySelector('.explorer-toolbar')?.classList.contains('tree-overlay')).toBe(true);
    const focus = screen.getByTestId('tree-focus');
    expect(focus.classList.contains('tree-overlay')).toBe(true);
    expect(focus.getAttribute('aria-labelledby')).toBeTruthy();
    const toggle = screen.getByTestId('tree-focus-toggle');
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(toggle.textContent).toBe(tr('tree:focus.hide'));
    fireEvent.click(toggle);
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(focus.dataset.open).toBe('false');
    expect(toggle.textContent).toBe(tr('tree:focus.show'));
    fireEvent.click(toggle);
    expect(focus.dataset.open).toBe('true');
  });

  it('a drag is not a click', () => {
    const a = show();
    const svg = screen.getByTestId('explorer-svg');
    const before = svg.getAttribute('viewBox');
    fireEvent.pointerDown(svg, {
      clientX: 10,
      clientY: 10,
      pointerId: 1,
      isPrimary: true,
      button: 0,
      pointerType: 'mouse',
    });
    fireEvent.pointerMove(svg, { clientX: 60, clientY: 40, pointerId: 1 });
    fireEvent.pointerUp(svg, { pointerId: 1 });
    expect(svg.getAttribute('viewBox')).not.toBe(before);
    fireEvent.click(node('c2'));
    expect(a.openTree).not.toHaveBeenCalled();
  });

  it('zooms with a pinch', () => {
    show();
    const canvas = screen.getByTestId('explorer-canvas');
    const svg = screen.getByTestId('explorer-svg');
    fireEvent.pointerDown(svg, {
      clientX: 100,
      clientY: 100,
      pointerId: 1,
      isPrimary: true,
      pointerType: 'touch',
    });
    fireEvent.pointerDown(svg, {
      clientX: 200,
      clientY: 100,
      pointerId: 2,
      isPrimary: false,
      pointerType: 'touch',
    });
    fireEvent.pointerMove(svg, { clientX: 250, clientY: 100, pointerId: 2 });
    expect(canvas.dataset.zoom).toBe('1.50');
  });

  it('loads the rest of a generation from "+N more", and earlier / later generations', () => {
    const a = show(tree({ hasEarlier: true, hasLater: true }));
    const more = screen.getByTestId('explorer-more');
    expect(more.getAttribute('aria-label')).toBe(tr('explorer:canvas.moreLabel', { count: 4 }));
    fireEvent.click(more);
    expect(a.more).toHaveBeenCalledWith(1, 'f', 'cur-1');
    fireEvent.click(screen.getByTestId('tree-earlier'));
    fireEvent.click(screen.getByTestId('tree-later'));
    expect(a.loadEarlier).toHaveBeenCalled();
    expect(a.loadLater).toHaveBeenCalled();
  });

  it('routes the focused post actions to the callbacks', () => {
    const a = show();
    fireEvent.click(within(screen.getByTestId('tree-focus')).getByTestId('feed-reply'));
    expect(a.reply).toHaveBeenCalledWith('f');
  });

  it('zooms by buttons and keys, pans by keys, fits and returns to the focal post', () => {
    show();
    const canvas = screen.getByTestId('explorer-canvas');
    const svg = screen.getByTestId('explorer-svg');
    expect(canvas.dataset.zoom).toBe('1.00');
    const home = svg.getAttribute('viewBox');
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
    fireEvent.click(screen.getByTestId('explorer-zoom-in'));
    fireEvent.click(screen.getByTestId('explorer-recenter'));
    const [hx, hy] = (home ?? '').split(' ').map(Number);
    const [x, y, w, h] = (svg.getAttribute('viewBox') ?? '').split(' ').map(Number);
    const [, , hw = 0, hh = 0] = (home ?? '').split(' ').map(Number);
    expect((x ?? 0) + (w ?? 0) / 2).toBeCloseTo((hx ?? 0) + hw / 2);
    expect((y ?? 0) + (h ?? 0) / 2).toBeCloseTo((hy ?? 0) + hh / 2);
  });

  it('shows the minimap on desktop once zoomed out, and moves the view from it', () => {
    show();
    expect(screen.queryByTestId('explorer-minimap')).toBeNull();
    fireEvent.click(screen.getByTestId('explorer-zoom-out'));
    const map = screen.getByTestId('explorer-minimap');
    expect(map.getAttribute('aria-hidden')).toBe('true');
    const svg = screen.getByTestId('explorer-svg');
    const before = svg.getAttribute('viewBox');
    vi.spyOn(map, 'getBoundingClientRect').mockReturnValue({
      left: 0,
      top: 0,
      width: 176,
      height: 112,
      right: 176,
      bottom: 112,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    });
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
