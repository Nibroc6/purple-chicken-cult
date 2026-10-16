// The seven integrity components (§15.5): tier headers and chains from props
// only, the inviter label as a non-interactive tag with "report the inviter"
// routed through the invitee, the lazy "why" panel through the injected loader,
// the audience badge and edited mark, the report action — and every visible
// string from the catalogs (a pseudo-locale pass finds no literal text).
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterAll, describe, expect, it, vi } from 'vitest';
import { i18n } from '../i18n/setup';
import { asRef, type ChainVM } from '../vm/types';
import {
  AudienceBadge,
  Chain,
  EditedMark,
  InviterLabel,
  ReportAction,
  TierHeader,
  WhyButton,
  WhyLoaderContext,
  integrityTestIds,
  type WhyLoader,
  type WhyVM,
} from './index';
import { shiftArgs } from './WhyPanel';

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

const chain = (over: Partial<ChainVM> = {}): ChainVM => ({ dist: 1, middle: [], hiddenMiddle: 0, firstHopPlus: false, ...over });
const bob = asRef('r-bob');
const dave = asRef('r-dave');

const why: WhyVM = {
  tier: 3,
  via: chain({ dist: 3, firstHop: { ref: bob, handle: 'bob' }, hiddenMiddle: 1 }),
  postedAt: new Date(Date.now() - 2 * 3600_000),
  preset: 'balanced',
  shiftMinutes: 150,
  reasons: ['network_likes', 'reply_penalty'],
};

function withLoader(load: WhyLoader | undefined, node: ReactNode) {
  return render(<WhyLoaderContext.Provider value={load}>{node}</WhyLoaderContext.Provider>);
}

describe('TierHeader', () => {
  it('renders exactly the catalog text for each kind', () => {
    render(
      <>
        <TierHeader tier={1} kind="start" />
        <TierHeader tier={4} kind="end" />
        <TierHeader tier={4} kind="caught_up" />
      </>,
    );
    const hs = screen.getAllByTestId('integrity-tier-header');
    expect(hs.map((h) => h.textContent)).toEqual([
      tr('integrity:tier.start', { tier: 1 }),
      tr('integrity:tier.end', { tier: 4 }),
      tr('integrity:tier.caughtUp'),
    ]);
    expect(hs.map((h) => h.dataset.kind)).toEqual(['start', 'end', 'caught_up']);
    expect(hs[0]?.tagName).toBe('H2');
  });
});

describe('Chain', () => {
  const text = (c: ChainVM) => {
    const { unmount } = render(<Chain chain={c} author={{ ref: dave, handle: 'dave' }} />);
    const s = screen.getByTestId('integrity-chain').textContent;
    unmount();
    return s;
  };
  it('names the first hop and the author, collapses the hidden middle', () => {
    expect(text(chain({ dist: 1 }))).toBe('you → @dave');
    expect(text(chain({ dist: 3, firstHop: { ref: bob, handle: 'bob' }, hiddenMiddle: 1 }))).toBe('you → @bob → 1 other → @dave');
    expect(
      text(chain({ dist: 4, firstHop: { ref: bob, handle: 'bob' }, middle: [{ ref: asRef('c'), handle: 'carol' }], hiddenMiddle: 2 })),
    ).toBe('you → @bob → @carol → 2 others → @dave');
    expect(text(chain({ dist: 0 }))).toBe('You');
  });
  it('is a labelled group and never a link', () => {
    render(<Chain chain={chain({ dist: 2, firstHop: { ref: bob, handle: 'bob' } })} author={{ ref: dave, handle: 'dave' }} />);
    const c = screen.getByRole('group', { name: tr('integrity:chain.label') });
    expect(c.querySelector('a')).toBeNull();
    expect(c.dataset.dist).toBe('2');
  });
});

describe('InviterLabel', () => {
  it('is a tag; reporting the inviter routes through the invitee', () => {
    const onReport = vi.fn();
    render(<InviterLabel inviter={{ kind: 'user', handle: 'olga' }} invitee={dave} onReport={onReport} />);
    const label = screen.getByTestId('integrity-inviter-label');
    expect(label.textContent).toBe(tr('integrity:inviter.label', { kind: 'user', handle: 'olga' }));
    expect(label.closest('a')).toBeNull();
    expect(label.querySelector('a,button')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: tr('integrity:inviter.report') }));
    expect(onReport).toHaveBeenCalledWith({ kind: 'inviter', ref: dave });
  });
  it('has nothing to report for genesis or a deleted inviter', () => {
    render(
      <>
        <InviterLabel inviter={{ kind: 'genesis' }} invitee={dave} onReport={vi.fn()} />
        <InviterLabel inviter={{ kind: 'deleted' }} invitee={dave} onReport={vi.fn()} />
      </>,
    );
    expect(screen.queryByTestId('integrity-inviter-report')).toBeNull();
    expect(screen.getAllByTestId('integrity-inviter-label').map((l) => l.textContent)).toEqual([
      tr('integrity:inviter.label', { kind: 'genesis', handle: '' }),
      tr('integrity:inviter.label', { kind: 'deleted', handle: '' }),
    ]);
  });
});

describe('WhyButton', () => {
  it('loads lazily through the injected loader, once', async () => {
    const load = vi.fn<WhyLoader>(() => Promise.resolve(why));
    withLoader(load, <WhyButton post={dave} />);
    expect(load).not.toHaveBeenCalled();
    const btn = screen.getByRole('button', { name: tr('integrity:why.button') });
    expect(btn.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(btn);
    expect(load).toHaveBeenCalledWith(dave);
    expect(screen.getByTestId('integrity-why-loading')).toBeTruthy();
    const body = await screen.findByTestId('integrity-why-body');
    expect(within(body).getByTestId('integrity-why-tier').textContent).toBe(tr('integrity:why.tier', { tier: 3 }));
    expect(within(body).getByTestId('integrity-why-chain').textContent).toBe(`you → @bob → 1 other → ${tr('integrity:why.author')}`);
    expect(within(body).getByTestId('integrity-why-preset').textContent).toBe(tr('integrity:why.preset', { preset: 'balanced' }));
    expect(within(body).getByTestId('integrity-why-shift').textContent).toBe(
      tr('integrity:why.shift', { dir: 'up', unit: 'h', minutes: 150, hours: 3 }),
    );
    expect(within(body).getAllByRole('listitem').map((li) => li.textContent)).toEqual([
      tr('integrity:why.reason.network_likes'),
      tr('integrity:why.reason.reply_penalty'),
    ]);
    expect(btn.getAttribute('aria-expanded')).toBe('true');
    // Escape closes and returns focus; reopening does not fetch again.
    fireEvent.keyDown(screen.getByTestId('integrity-why-panel'), { key: 'Escape' });
    expect(screen.queryByTestId('integrity-why-panel')).toBeNull();
    expect(document.activeElement).toBe(btn);
    fireEvent.click(btn);
    expect(await screen.findByTestId('integrity-why-body')).toBeTruthy();
    expect(load).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByTestId('integrity-why-close'));
    expect(screen.queryByTestId('integrity-why-panel')).toBeNull();
  });
  it('offers a retry after a failure', async () => {
    const load = vi.fn<WhyLoader>().mockRejectedValueOnce(new Error('x')).mockResolvedValueOnce(why);
    withLoader(load, <WhyButton post={dave} />);
    fireEvent.click(screen.getByTestId('integrity-why'));
    const alert = await screen.findByRole('alert');
    fireEvent.click(within(alert).getByRole('button', { name: tr('integrity:why.retry') }));
    expect(await screen.findByTestId('integrity-why-body')).toBeTruthy();
    expect(load).toHaveBeenCalledTimes(2);
  });
  it('has nothing to open without a loader', () => {
    withLoader(undefined, <WhyButton post={dave} />);
    const btn = screen.getByTestId('integrity-why');
    expect(btn.getAttribute('aria-disabled')).toBe('true');
    fireEvent.click(btn);
    expect(screen.queryByTestId('integrity-why-panel')).toBeNull();
  });
  it('words the ranking shift', () => {
    expect(shiftArgs(0)).toEqual({ dir: 'none', unit: 'm', minutes: 0, hours: 0 });
    expect(shiftArgs(-45)).toEqual({ dir: 'down', unit: 'm', minutes: 45, hours: 1 });
    expect(tr('integrity:why.shift', shiftArgs(-45))).toBe('Moved down by 45 minutes');
    expect(tr('integrity:why.shift', shiftArgs(61))).toBe('Moved up by about 1 hour');
  });
});

describe('AudienceBadge, EditedMark and ReportAction', () => {
  it('names the audience; a forced one carries the note for its author', () => {
    render(
      <>
        <AudienceBadge audience="d2" />
        <AudienceBadge audience="d1" forced />
      </>,
    );
    const [a, b] = screen.getAllByTestId('integrity-audience');
    expect(a?.textContent).toBe(tr('integrity:audience.label', { audience: 'd2' }));
    expect(a?.dataset.audience).toBe('d2');
    expect(b?.dataset.forced).toBe('true');
    expect(b?.textContent).toBe(tr('integrity:audience.label', { audience: 'd1' }) + tr('integrity:audience.forced'));
  });
  it('shows the edited mark only for an edited post, with its time', () => {
    const at = new Date('2026-01-02T03:04:05Z');
    render(
      <>
        <EditedMark />
        <EditedMark editedAt={at} />
      </>,
    );
    const [plain, edited] = screen.getAllByTestId('integrity-edited');
    expect(plain?.hidden).toBe(true);
    expect(edited?.hidden).toBe(false);
    expect(edited?.querySelector('time')?.getAttribute('datetime')).toBe(at.toISOString());
    expect(edited?.title).toContain(tr('integrity:edited').slice(1));
  });
  it('hands the target to the report flow', () => {
    const onReport = vi.fn();
    render(<ReportAction target={{ kind: 'post', ref: dave }} onReport={onReport} />);
    fireEvent.click(screen.getByRole('button', { name: tr('integrity:report.action') }));
    expect(onReport).toHaveBeenCalledWith({ kind: 'post', ref: dave });
  });
});

describe('catalogs only', () => {
  afterAll(async () => {
    await act(() => i18n.changeLanguage('en'));
  });
  it('renders every visible string from the catalogs (pseudo-locale)', async () => {
    await act(async () => {
      await i18n.changeLanguage('en-XA');
      await i18n.loadNamespaces('integrity');
    });
    const load: WhyLoader = () => Promise.resolve(why);
    withLoader(
      load,
      <>
        <TierHeader tier={2} kind="start" />
        <Chain chain={why.via} author={{ ref: dave, handle: 'dave' }} />
        <InviterLabel inviter={{ kind: 'user', handle: 'olga' }} invitee={dave} onReport={vi.fn()} />
        <WhyButton post={dave} />
        <AudienceBadge audience="d1" forced />
        <EditedMark editedAt={new Date()} />
        <ReportAction target={{ kind: 'post', ref: dave }} onReport={vi.fn()} />
      </>,
    );
    fireEvent.click(screen.getByTestId('integrity-why'));
    await screen.findByTestId('integrity-why-body');
    for (const id of integrityTestIds) expect(screen.getAllByTestId(id).length).toBeGreaterThan(0);
    const roots = [...document.querySelectorAll<HTMLElement>('.integrity')];
    const texts: string[] = [];
    for (const root of roots) {
      const walk = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
      for (let n = walk.nextNode(); n; n = walk.nextNode()) {
        const s = n.textContent?.trim() ?? '';
        // Relative times come from Intl (lib/time), not from a catalog.
        if (s && n.parentElement?.tagName !== 'TIME') texts.push(s);
      }
      for (const attr of ['aria-label', 'title']) {
        const v = root.getAttribute(attr);
        if (v) texts.push(v);
      }
    }
    expect(texts.length).toBeGreaterThan(15);
    for (const s of texts) expect(s, s).toMatch(/^\[.*\]$/s);
    await waitFor(() => {
      expect(screen.getByTestId('integrity-tier-header').textContent).toMatch(/^\[/);
    });
  });
});
