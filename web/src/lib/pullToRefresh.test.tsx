// Pull to refresh: a long enough pull at the top refreshes on release; a short
// one, one away from the top, a cancelled one or one under a dialog does not.
import { fireEvent, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { PULL_TRIGGER, usePullToRefresh } from './pullToRefresh';

function pull(dy: number, end: 'touchEnd' | 'touchCancel' = 'touchEnd') {
  fireEvent.touchStart(window, { touches: [{ clientY: 100 }] });
  fireEvent.touchMove(window, { touches: [{ clientY: 100 + dy / 2 }] });
  fireEvent.touchMove(window, { touches: [{ clientY: 100 + dy }] });
  fireEvent[end](window, { touches: [] });
}

describe('usePullToRefresh', () => {
  afterEach(() => {
    window.scrollY = 0;
    document.querySelector('dialog')?.remove();
  });

  it('refreshes after a pull past the trigger, reporting the distance on the way', () => {
    const refresh = vi.fn();
    const seen: number[] = [];
    renderHook(() => {
      usePullToRefresh(refresh, (d) => seen.push(d));
    });
    pull(PULL_TRIGGER * 3);
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(Math.max(...seen)).toBeGreaterThanOrEqual(PULL_TRIGGER);
    expect(seen.at(-1)).toBe(0);
  });

  it('does nothing for a short pull, a cancelled one, below the top or under a dialog', () => {
    const refresh = vi.fn();
    renderHook(() => {
      usePullToRefresh(refresh, () => undefined);
    });
    pull(PULL_TRIGGER);
    pull(PULL_TRIGGER * 3, 'touchCancel');
    window.scrollY = 200;
    pull(PULL_TRIGGER * 3);
    window.scrollY = 0;
    const d = document.createElement('dialog');
    d.setAttribute('open', '');
    document.body.append(d);
    pull(PULL_TRIGGER * 3);
    expect(refresh).not.toHaveBeenCalled();
  });
});
