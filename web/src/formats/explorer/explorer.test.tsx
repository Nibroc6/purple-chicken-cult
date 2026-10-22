// Explorer: the feed card's tree thumbnail opening the tree (the reply-tree
// canvas every format shares is tested in formats/tree).
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import { i18n } from '../../i18n/setup';
import { postVM } from '../../test/fixtures';
import { asRef } from '../../vm/types';
import type { TreeActions } from '../types';
import explorer from './index';

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

describe('explorer feed card', () => {
  it('carries a tree thumbnail that opens the tree', () => {
    const a = actions();
    const post = postVM({
      replyCount: 7,
      context: [{ ref: asRef('x'), kind: 'ref', authorHandle: 'bob', excerpt: 'hi' }],
    });
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
