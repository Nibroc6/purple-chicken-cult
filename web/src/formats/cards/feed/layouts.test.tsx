// Cards and Compact feed and post layouts: every integrity element present,
// tier headers only from the API's markers, the seen target on a card's own
// post only (never context rows or earlier thread parts), folding by keyboard
// and screen reader, collapsed groups, load more, hidden posts, tier sections.
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { resetEngageStore } from '../../../features/engage/store';
import { setHidden } from '../../../features/engage/store';
import { clearURLs } from '../../../features/media/urls';
import { i18n } from '../../../i18n/setup';
import { integrityTestIds } from '../../../integrity/types';
import { postVM } from '../../../test/fixtures';
import { asRef, type FeedPageVM, type PostVM } from '../../../vm/types';
import type { FormatModule, PostActions } from '../../types';
import cards from '../index';
import compact from '../../compact/index';
import { groupTiers, shouldFold } from './parts';

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

function actions(): PostActions {
  return {
    like: vi.fn(),
    reply: vi.fn(),
    openTree: vi.fn(),
    hide: vi.fn(),
    showLess: vi.fn(),
    report: vi.fn(),
    markSeen: vi.fn(),
  };
}

const p = (ref: string, over: Partial<PostVM> = {}) => postVM({ ref: asRef(ref), ...over });

const long = { source: 'x', nodes: Array.from({ length: 9 }, (_, i) => `line ${String(i)}`) };

function pages(): FeedPageVM[] {
  return [
    {
      session: 's1',
      preset: 'balanced',
      items: [
        { type: 'tier_start', tier: 1 },
        {
          type: 'post',
          post: p('a', {
            tier: 1,
            body: { source: 'Friends of friends', nodes: ['Friends of friends'] },
            context: [{ ref: asRef('parent'), kind: 'ref', authorHandle: 'carol', excerpt: 'the parent' }],
          }),
        },
        { type: 'thread', parts: [p('t1', { thread: { index: 1, total: 2 } }), p('t2', { thread: { index: 2, total: 2 } })] },
      ],
      next: 'cursor-1',
    },
    {
      session: 's1',
      preset: 'balanced',
      items: [
        { type: 'post', post: p('b', { tier: 1, fold: 'card', body: long }) },
        { type: 'tier_end', tier: 1 },
        { type: 'tier_start', tier: 3 },
        { type: 'collapsed', tier: 3, count: 2, token: 'tok' },
        { type: 'tier_end', tier: 3 },
        { type: 'caught_up' },
      ],
    },
  ];
}

beforeEach(() => {
  resetEngageStore();
});

afterEach(() => {
  clearURLs();
  vi.unstubAllGlobals();
});

describe('tier sections', () => {
  it('keep a tier continued on a later page in one section', () => {
    const { sections, caughtUp } = groupTiers(pages());
    expect(caughtUp).toBe(true);
    expect(sections.map((s) => [s.tier, s.start, s.items.length, s.end])).toEqual([
      [1, true, 3, true],
      [3, true, 1, true],
    ]);
  });

  it('put items before any marker in an unlabelled section', () => {
    const { sections } = groupTiers([{ session: 's', preset: 'latest', items: [{ type: 'post', post: p('x') }, { type: 'tier_end', tier: 2 }] }]);
    expect(sections.map((s) => [s.tier, s.start, s.end])).toEqual([
      [0, false, false],
      [2, false, true],
    ]);
  });

  it('fold feed cards, tree posts at their own limit and never the focal post', () => {
    expect(shouldFold(p('x', { fold: 'card' }), 'feed')).toBe('card');
    expect(shouldFold(p('x', { fold: 'none' }), 'feed')).toBeUndefined();
    expect(shouldFold(p('x', { fold: 'card' }), 'tree')).toBeUndefined();
    expect(shouldFold(p('x', { fold: 'tree' }), 'tree')).toBe('tree');
    expect(shouldFold(p('x', { fold: 'tree' }), 'focal')).toBeUndefined();
  });
});

describe.each([
  ['cards', cards],
  ['compact', compact],
] as [string, FormatModule][])('%s layouts', (id, f) => {
  function show(pg: FeedPageVM[] = pages(), a: PostActions = actions()) {
    const onLoadMore = vi.fn();
    const onExpand = vi.fn();
    const r = render(
      <MemoryRouter>
        <f.FeedLayout page={pg} actions={a} onLoadMore={onLoadMore} onExpand={onExpand} />
      </MemoryRouter>,
    );
    return { ...r, onLoadMore, onExpand, a };
  }

  it('renders every integrity element', () => {
    show();
    expect(screen.getByTestId('feed-layout').dataset.format).toBe(id);
    for (const tid of integrityTestIds) expect(screen.getAllByTestId(tid).length).toBeGreaterThan(0);
  });

  it('takes tier headers only from the markers, with catalog text', () => {
    show();
    const headers = screen.getAllByTestId('integrity-tier-header');
    // tier 1 start + end, tier 3 start + end, caught up: never one for the post saying "Friends of friends".
    expect(headers.map((h) => h.textContent)).toEqual([
      tr('integrity:tier.start', { tier: 1 }),
      tr('integrity:tier.end', { tier: 1 }),
      tr('integrity:tier.start', { tier: 3 }),
      tr('integrity:tier.end', { tier: 3 }),
      tr('integrity:tier.caughtUp'),
    ]);
    expect(screen.getAllByTestId('feed-tier').map((s) => s.dataset.tier)).toEqual(['1', '3']);
  });

  it("puts the seen target on each card's own post only", () => {
    const { container } = show();
    const targets = [...container.querySelectorAll<HTMLElement>('[data-seen-ref]')].map((e) => e.dataset.seenRef);
    expect(targets).toEqual(['a', 't2', 'b']);
    for (const row of screen.getAllByTestId('feed-context')) expect(row.closest('[data-seen-ref]')?.getAttribute('data-seen-ref')).toBe('a');
    expect(row(container, 't1').hasAttribute('data-seen-ref')).toBe(false);
  });

  it('stacks a thread: earlier parts unfold on request, the ranked part has the actions', () => {
    const { container } = show();
    const thread = screen.getByTestId('feed-thread');
    const toggle = within(thread).getByTestId('feed-thread-toggle');
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(row(container, 't1').closest('ol')?.hidden).toBe(true);
    fireEvent.click(toggle);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(row(container, 't1').closest('ol')?.hidden).toBe(false);
    expect(within(row(container, 't1')).queryByTestId('engage')).toBeNull();
    expect(within(row(container, 't2')).getByTestId('engage')).toBeTruthy();
    expect(within(row(container, 't2')).getByTestId('feed-thread-pos').textContent).toBe('2/2');
  });

  it('folds long posts behind a button that keyboard and screen readers can use', () => {
    const { container } = show();
    const b = row(container, 'b');
    const toggle = within(b).getByTestId('feed-fold-toggle');
    expect(toggle.tagName).toBe('BUTTON');
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    const body = document.getElementById(toggle.getAttribute('aria-controls') ?? '');
    expect(body?.className).toContain('fold-card');
    fireEvent.click(toggle);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(body?.className).not.toContain('fold-clamp');
    expect(within(row(container, 'a')).queryByTestId('feed-fold-toggle')).toBeNull();
  });

  it('shows context rows from the view-model', () => {
    show();
    const [ctx] = screen.getAllByTestId('feed-context');
    expect(ctx?.textContent).toBe(`${tr('feed:context.ref', { handle: 'carol' })} the parent`);
  });

  it('expands a collapsed group and loads more on request', () => {
    const { onExpand, unmount } = show();
    const group = screen.getByTestId('feed-collapsed');
    expect(group.dataset.count).toBe('2');
    fireEvent.click(within(group).getByTestId('feed-expand'));
    expect(onExpand).toHaveBeenCalledWith('tok');
    // The last page has no next: no "load more".
    expect(screen.queryByTestId('feed-more')).toBeNull();
    unmount();
    const { onLoadMore } = show(pages().slice(0, 1));
    fireEvent.click(screen.getByTestId('feed-more'));
    expect(onLoadMore).toHaveBeenCalledTimes(1);
  });

  it('routes post actions to the callbacks', () => {
    const { container, a } = show();
    const card = row(container, 'a');
    fireEvent.click(within(card).getByTestId('feed-reply'));
    fireEvent.click(within(card).getByTestId('feed-tree'));
    fireEvent.click(within(card).getByTestId('engage-like'));
    fireEvent.click(within(card).getByTestId('integrity-report'));
    expect(a.reply).toHaveBeenCalledWith('a');
    expect(a.openTree).toHaveBeenCalledWith('a');
    expect(a.like).toHaveBeenCalledWith('a', true);
    expect(a.report).toHaveBeenCalledWith({ kind: 'post', ref: 'a' });
  });

  it('drops the body and the seen target of a post hidden in this session', () => {
    const { container } = show();
    act(() => {
      setHidden('a', 'hide');
    });
    const card = row(container, 'a');
    expect(card.hasAttribute('data-seen-ref')).toBe(false);
    expect(within(card).queryByTestId('feed-context')).toBeNull();
    expect(within(card).getByTestId('engage-hidden')).toBeTruthy();
    expect(within(card).getByTestId('integrity-chain')).toBeTruthy();
  });

  it('renders post layouts outside a router and a feed (no seen target)', () => {
    const { container } = render(<f.PostLayout post={p('solo')} actions={actions()} variant="tree" />);
    expect(container.querySelector('[data-seen-ref]')).toBeNull();
    expect(container.querySelector('a[href="/people/u1"]')).toBeTruthy();
    for (const tid of integrityTestIds.filter((x) => x !== 'integrity-tier-header')) {
      expect(screen.getAllByTestId(tid).length).toBeGreaterThan(0);
    }
  });

  it('shows pictures in order with their alt text: display size in the feed, thumbs in tree rows', async () => {
    const asked: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn((req: Request) => {
        const u = new URL(req.url);
        asked.push(`${u.pathname}?${u.searchParams.toString()}`);
        return Promise.resolve(
          new Response(JSON.stringify({ url: `/m/k?e=1`, expires_at: '2099-01-01T00:00:00Z' }), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          }),
        );
      }),
    );
    const media = [
      { ref: asRef('m1'), w: 40, h: 30, alt: 'first' },
      { ref: asRef('m2'), w: 40, h: 30 },
    ];
    const image = { ref: asRef('pi'), w: 20, h: 10, alt: 'site' };
    const post = p('pic', { media, previews: [{ url: 'https://example.com/', host: 'example.com', hostUnicode: 'example.com', lookalike: false, image }] });
    const { unmount } = render(<f.PostLayout post={post} actions={actions()} variant="feed" />);
    const imgs = within(screen.getByTestId('feed-media')).getAllByTestId('media-image');
    expect(imgs.map((i) => [i.dataset.variant, i.getAttribute('alt')])).toEqual([
      ['display', 'first'],
      ['display', tr('media:image.noAlt')],
    ]);
    expect(within(screen.getByTestId('feed-preview-images')).getByTestId('media-image').dataset.variant).toBe('thumb');
    await vi.waitFor(() => {
      expect(asked).toContain('/api/media/m1/url?variant=display');
    });
    unmount();
    render(<f.PostLayout post={post} actions={actions()} variant="tree" />);
    expect(within(screen.getByTestId('feed-media')).getAllByTestId('media-image').map((i) => i.dataset.variant)).toEqual(['thumb', 'thumb']);
    render(<f.PostLayout post={p('none')} actions={actions()} variant="feed" />);
    expect(screen.getAllByTestId('feed-media')).toHaveLength(1);
  });
});

function row(container: HTMLElement, ref: string): HTMLElement {
  const el = container.querySelector<HTMLElement>(`[data-testid="feed-post"][data-ref="${ref}"]`);
  if (!el) throw new Error(`no post ${ref}`);
  return el;
}
