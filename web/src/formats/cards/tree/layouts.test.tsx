// Cards, Compact and Explorer family-tree layouts: every integrity element present, a
// section header per row from its label, the focal post never folded while
// tree posts longer than 10 lines fold behind a keyboard / screen-reader
// button, "+N more" after five (N counts the row's visible posts beyond them),
// the ranked list with "load more", "shown above" stubs, "also replies to"
// chips that refocus, tier hairlines, re-centering by buttons (keyboard), the
// desktop sides, earlier / later rows, and no seen targets outside a feed.
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { resetEngageStore } from '../../../features/engage/store';
import { i18n } from '../../../i18n/setup';
import { integrityTestIds } from '../../../integrity/types';
import { postVM } from '../../../test/fixtures';
import { asRef, type PostVM, type TreeItemVM, type TreeRowVM, type TreeVM } from '../../../vm/types';
import type { FormatModule, TreeActions } from '../../types';
import cards from '../index';
import compact from '../../compact/index';
import explorer from '../../explorer/index';
import { side } from '../TreeLayout';
import { extraCount, VISIBLE } from './parts';

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

const long = { source: 'x', nodes: Array.from({ length: 14 }, (_, i) => `line ${String(i)}`) };

const item = (ref: string, over: Partial<TreeItemVM> = {}, post: Partial<PostVM> = {}): TreeItemVM => ({
  post: postVM({ ref: asRef(ref), body: { source: ref, nodes: [ref] }, ...post }),
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
    hasEarlier: true,
    hasLater: true,
    rows: [
      row(-1, 'replying_to', 'f', [item('p1'), item('p2')]),
      row(0, 'this_post', 'p1', [
        item('f', {}, { body: long, fold: 'tree', tier: 0 }),
        item('s1', { tierDivider: true }, { tier: 1 }),
      ]),
      row(
        1,
        'replies',
        'f',
        [
          item('c1', {}, { fold: 'tree', body: long }),
          item('c2', { alsoRepliesTo: [{ ref: asRef('u-b'), handle: 'bob' }], alsoParents: [asRef('p2')] }),
          item('c3'),
          item('c4'),
          item('c5', { tierDivider: true }, { tier: 3 }),
          item('c6'),
          item('c7'),
        ],
        4,
        'cur-1',
      ),
      row(2, 'replies_to_replies', 'c1', [item('p2', { shownAbove: true }, { body: null, author: { ...postVM().author, handle: 'fran' } })]),
      row(3, 'generations_down', 'g', [item('g3')]),
    ],
    ...over,
  };
}

beforeEach(() => {
  resetEngageStore();
});

describe('tree helpers', () => {
  it('counts "+N more" from visible posts only and places desktop sides', () => {
    expect(extraCount(row(1, 'replies', 'f', [item('a')], 0))).toBe(0);
    expect(extraCount(row(1, 'replies', 'f', Array.from({ length: 7 }, (_, i) => item(`x${String(i)}`)), 4))).toBe(6);
    expect([0, 1, 2, 3, 4].map(side)).toEqual(['center', 'r1', 'l1', 'r2', 'l2']);
    expect(VISIBLE).toBe(5);
  });
});

describe.each([
  ['cards', cards],
  ['compact', compact],
  ['explorer', explorer],
] as [string, FormatModule][])('%s tree layout', (id, f) => {
  function show(tv: TreeVM = tree(), a: TreeActions = actions()) {
    const r = render(
      <MemoryRouter>
        <f.TreeLayout tree={tv} actions={a} />
      </MemoryRouter>,
    );
    return { ...r, a };
  }

  const itemOf = (ref: string) => {
    const el = document.querySelector<HTMLElement>(`[data-testid="tree-item"][data-ref="${ref}"]`);
    if (!el) throw new Error(`no item ${ref}`);
    return el;
  };
  const rowOf = (gen: number) => {
    const el = document.querySelector<HTMLElement>(`[data-testid="tree-row"][data-gen="${String(gen)}"]`);
    if (!el) throw new Error(`no row ${String(gen)}`);
    return el;
  };

  it('renders every integrity element a post carries', () => {
    show();
    expect(screen.getByTestId('tree-layout').dataset.format).toBe(id);
    for (const tid of integrityTestIds.filter((t) => t !== 'integrity-tier-header')) {
      expect(screen.getAllByTestId(tid).length).toBeGreaterThan(0);
    }
  });

  it('puts a section header on every row, from the row label', () => {
    show();
    expect(screen.getAllByTestId('tree-row-header').map((h) => h.textContent)).toEqual([
      tr('tree:row.replying_to'),
      tr('tree:row.this_post'),
      tr('tree:row.replies'),
      tr('tree:row.replies_to_replies'),
      tr('tree:row.generations_down', { count: 3 }),
    ]);
    expect(screen.getAllByTestId('tree-row').map((r) => r.dataset.label)).toEqual([
      'replying_to',
      'this_post',
      'replies',
      'replies_to_replies',
      'generations_down',
    ]);
  });

  it('centers the first post of each row and shows five before "+N more"', () => {
    show();
    expect(within(rowOf(1)).getAllByTestId('tree-item')).toHaveLength(VISIBLE);
    expect(itemOf('c1').dataset.centered).toBe('true');
    expect(itemOf('c2').dataset.centered).toBe('false');
    const chip = within(rowOf(1)).getByTestId('tree-more');
    expect(chip.dataset.count).toBe('6');
    expect(chip.textContent).toBe(tr('tree:row.more', { count: 6 }));
    expect(within(rowOf(0)).queryByTestId('tree-more')).toBeNull();
  });

  it('opens the rest of the row as a ranked list and loads more from the cursor', () => {
    const { a } = show();
    const chip = within(rowOf(1)).getByTestId('tree-more');
    expect(chip.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(chip);
    expect(chip.getAttribute('aria-expanded')).toBe('true');
    const list = within(rowOf(1)).getByTestId('tree-more-list');
    expect(within(list).getAllByTestId('tree-item').map((e) => e.dataset.ref)).toEqual(['c6', 'c7']);
    fireEvent.click(within(list).getByTestId('tree-load-more'));
    expect(a.more).toHaveBeenCalledWith(1, 'f', 'cur-1');
  });

  it('never folds the focal post and folds tree posts past 10 lines by keyboard and screen reader', () => {
    show();
    expect(within(itemOf('f')).queryByTestId('feed-fold-toggle')).toBeNull();
    expect(within(itemOf('f')).getByTestId('feed-post').className).toContain('post-focal');
    const toggle = within(itemOf('c1')).getByTestId('feed-fold-toggle');
    expect(toggle.tagName).toBe('BUTTON');
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    const body = document.getElementById(toggle.getAttribute('aria-controls') ?? '');
    expect(body?.className).toContain('fold-tree');
    toggle.focus();
    expect(document.activeElement).toBe(toggle);
    fireEvent.click(toggle);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(body?.className).not.toContain('fold-clamp');
    expect(within(itemOf('c2')).queryByTestId('feed-fold-toggle')).toBeNull();
  });

  it('shows a post already above as "shown above", without its body', () => {
    show();
    const stub = within(rowOf(2)).getByTestId('tree-shown-above');
    expect(stub.textContent).toBe(tr('tree:item.shownAbove', { handle: 'fran' }));
    expect(within(rowOf(2)).queryByTestId('feed-post')).toBeNull();
  });

  it('refocuses on another parent from its chip', () => {
    const { a } = show();
    const chip = within(itemOf('c2')).getByTestId('tree-also');
    expect(chip.textContent).toBe(tr('tree:item.also', { handle: 'bob' }));
    fireEvent.click(chip);
    expect(a.openTree).toHaveBeenCalledWith('p2');
  });

  it('draws a hairline where the tier changes inside a row', () => {
    show();
    const dividers = screen.getAllByTestId('tree-tier-divider');
    expect(dividers).toHaveLength(2);
    expect(within(itemOf('s1')).getByTestId('tree-tier-divider').getAttribute('aria-label')).toBe(tr('tree:divider', { tier: 1 }));
    expect(within(itemOf('c5')).getByTestId('tree-tier-divider').getAttribute('aria-label')).toBe(tr('tree:divider', { tier: 3 }));
  });

  it('loads earlier and later rows at the edges', () => {
    const { a, unmount } = show();
    fireEvent.click(screen.getByTestId('tree-earlier'));
    fireEvent.click(screen.getByTestId('tree-later'));
    expect(a.loadEarlier).toHaveBeenCalledTimes(1);
    expect(a.loadLater).toHaveBeenCalledTimes(1);
    unmount();
    show(tree({ hasEarlier: false, hasLater: false }));
    expect(screen.queryByTestId('tree-earlier')).toBeNull();
    expect(screen.queryByTestId('tree-later')).toBeNull();
  });

  it('has no seen targets (they belong to feeds)', () => {
    const { container } = show();
    expect(container.querySelectorAll('[data-seen-ref]')).toHaveLength(0);
  });

  it('routes post actions to the callbacks', () => {
    const { a } = show();
    fireEvent.click(within(itemOf('c3')).getByTestId('feed-tree'));
    expect(a.openTree).toHaveBeenCalledWith('c3');
    fireEvent.click(within(itemOf('c3')).getByTestId('feed-reply'));
    expect(a.reply).toHaveBeenCalledWith('c3');
  });
});

describe('cards rows', () => {
  it('re-center by the previous / next buttons (keyboard) and place sides', () => {
    const a = actions();
    render(
      <MemoryRouter>
        <cards.TreeLayout tree={tree()} actions={a} />
      </MemoryRouter>,
    );
    const r1 = document.querySelector<HTMLElement>('[data-testid="tree-row"][data-gen="1"]');
    if (!r1) throw new Error('no row');
    const prev = within(r1).getByTestId('tree-prev');
    const next = within(r1).getByTestId('tree-next');
    expect(prev).toHaveProperty('disabled', true);
    expect(next.getAttribute('aria-label')).toBe(tr('tree:row.next'));
    fireEvent.click(next);
    expect(a.recenter).toHaveBeenCalledWith(1, 'c2');
    const c2 = within(r1).getAllByTestId('tree-item')[1];
    expect(c2?.dataset.centered).toBe('true');
    expect(c2?.getAttribute('aria-current')).toBe('true');
    expect(prev).toHaveProperty('disabled', false);
    expect(within(r1).getAllByTestId('tree-item').map((e) => e.dataset.side)).toEqual(['center', 'r1', 'l1', 'r2', 'l2']);
    // A row of one post has no buttons.
    const r3 = document.querySelector<HTMLElement>('[data-testid="tree-row"][data-gen="3"]');
    expect(r3 && within(r3).queryByTestId('tree-next')).toBeNull();
  });
});

describe('compact outline', () => {
  it('collapses a generation and re-centers from "show its replies"', () => {
    const a = actions();
    render(
      <MemoryRouter>
        <compact.TreeLayout tree={tree()} actions={a} />
      </MemoryRouter>,
    );
    const r1 = document.querySelector<HTMLElement>('[data-testid="tree-row"][data-gen="1"]');
    if (!r1) throw new Error('no row');
    const toggle = within(r1).getByTestId('tree-row-toggle');
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    const items = document.getElementById(toggle.getAttribute('aria-controls') ?? '');
    fireEvent.click(toggle);
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(items?.hidden).toBe(true);
    fireEvent.click(toggle);
    const c3 = within(r1).getAllByTestId('tree-item')[2];
    if (!c3) throw new Error('no c3');
    fireEvent.click(within(c3).getByTestId('tree-center'));
    expect(a.recenter).toHaveBeenCalledWith(1, 'c3');
    expect(c3.getAttribute('aria-current')).toBe('true');
    // Rows two or more above the focal row stay put: no re-centering there.
    expect(screen.getAllByTestId('tree-row').map((r) => r.dataset.depth)).toEqual(['0', '1', '2', '3', '4']);
  });
});
