// Integrity presence suite (plan §6.1): every format renders all seven integrity
// elements. Visibility, size and contrast are asserted in Playwright (INT).
import { render } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { postIntegrityTestIds } from '../integrity/types';
import { feedPageVM } from '../test/fixtures';
import { formats } from './registry';
import type { FormatId, PostActions } from './types';

const actions: PostActions = {
  like: vi.fn(),
  reply: vi.fn(),
  openTree: vi.fn(),
  hide: vi.fn(),
  showLess: vi.fn(),
  report: vi.fn(),
};

describe.each(Object.keys(formats) as FormatId[])('format %s', (id) => {
  it('renders every integrity element in the feed', async () => {
    const { default: f } = await formats[id]();
    expect(f.id).toBe(id);
    const { getAllByTestId } = render(
      <f.FeedLayout page={[feedPageVM()]} actions={actions} onLoadMore={vi.fn()} onExpand={vi.fn()} />,
    );
    for (const tid of postIntegrityTestIds) {
      expect(getAllByTestId(tid).length).toBeGreaterThan(0);
    }
  });
});
