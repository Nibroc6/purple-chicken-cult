// Picking several posts for one reply: the store, the pick button and the tray.
import { act, fireEvent, render, screen } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, describe, expect, it } from 'vitest';
import { i18n } from '../../i18n/setup';
import { clearPicks, composeURL, getPicks, refFromLink, togglePick } from './picks';
import { ReplyPick, ReplyTray } from './ReplyPicks';

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;
const post = (ref: string) => ({ ref, author: { handle: `h${ref}` } });

afterEach(() => {
  clearPicks();
});

describe('reply picks', () => {
  it('keeps up to three distinct posts and builds the composer address', () => {
    for (const r of ['a', 'b', 'a', 'c', 'd']) togglePick({ ref: r, handle: r });
    expect(getPicks().map((p) => p.ref)).toEqual(['b', 'c', 'd']);
    expect(composeURL(['b', 'c', 'b', 'x', 'y'])).toBe('/compose?parent=b&parent=c&parent=x&kind=ref');
    expect(JSON.parse(sessionStorage.getItem('pcc.replyPicks') ?? '[]')).toHaveLength(3);
  });

  it('reads a post ref from a link or a bare ref', () => {
    expect(refFromLink(' https://pcc.example/p/AbC_9-x ')).toBe('AbC_9-x');
    expect(refFromLink('/p/abc/#top')).toBe('abc');
    expect(refFromLink('abc')).toBe('abc');
    expect(refFromLink('https://pcc.example/people/abc x')).toBeUndefined();
  });

  it('picks from a post, lists the picks in the tray and opens the composer with all of them', () => {
    const router = createMemoryRouter(
      [
        {
          path: '/',
          element: (
            <>
              <ReplyPick post={post('a')} />
              <ReplyTray />
            </>
          ),
        },
        { path: '/compose', element: <p>composer</p> },
      ],
      { initialEntries: ['/'] },
    );
    render(<RouterProvider router={router} />);
    expect(screen.queryByTestId('reply-tray')).toBeNull();
    fireEvent.click(screen.getByTestId('reply-pick'));
    expect(screen.getByTestId('reply-pick').getAttribute('aria-pressed')).toBe('true');
    act(() => {
      togglePick({ ref: 'b', handle: 'hb' });
    });
    expect(screen.getByTestId('reply-tray').textContent).toContain(tr('composer:picks.title', { count: 2, max: 3 }));
    fireEvent.click(screen.getByTestId('reply-tray-write'));
    expect(router.state.location.pathname + router.state.location.search).toBe('/compose?parent=a&parent=b&kind=ref');
    expect(getPicks()).toEqual([]);
  });
});
