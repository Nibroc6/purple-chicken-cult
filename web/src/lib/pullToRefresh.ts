// Pull to refresh for touch screens: at the top of the page, a finger pulled
// down past PULL_TRIGGER (after resistance) calls onRefresh when it lifts. An
// installed app on iOS has no pull to refresh of its own, and the browser's
// would reload the whole app; pages that use this turn the browser's off
// (overscroll-behavior in their CSS). onPull reports the distance while
// pulling (0 when idle), for an indicator; it never re-renders the page.
import { useEffect, useRef } from 'react';

export const PULL_TRIGGER = 72;
const RESISTANCE = 0.5;
const MAX_PULL = PULL_TRIGGER * 1.5;

export function usePullToRefresh(onRefresh: () => void, onPull: (distance: number) => void): void {
  const refresh = useRef(onRefresh);
  const pull = useRef(onPull);
  useEffect(() => {
    refresh.current = onRefresh;
    pull.current = onPull;
  });

  useEffect(() => {
    let startY: number | null = null;
    let distance = 0;
    const set = (d: number) => {
      if (d === distance) return;
      distance = d;
      pull.current(d);
    };
    const onStart = (e: TouchEvent) => {
      // One finger, at the very top, and no dialog over the page.
      const t = e.touches[0];
      startY = e.touches.length === 1 && t && window.scrollY <= 0 && !document.querySelector('dialog[open]') ? t.clientY : null;
      set(0);
    };
    const onMove = (e: TouchEvent) => {
      const t = e.touches[0];
      if (startY === null || !t) return;
      const dy = t.clientY - startY;
      set(dy > 0 && window.scrollY <= 0 ? Math.min(dy * RESISTANCE, MAX_PULL) : 0);
    };
    const onEnd = () => {
      if (startY === null) return;
      startY = null;
      const fire = distance >= PULL_TRIGGER;
      set(0);
      if (fire) refresh.current();
    };
    const onCancel = () => {
      startY = null;
      set(0);
    };
    window.addEventListener('touchstart', onStart, { passive: true });
    window.addEventListener('touchmove', onMove, { passive: true });
    window.addEventListener('touchend', onEnd);
    window.addEventListener('touchcancel', onCancel);
    return () => {
      window.removeEventListener('touchstart', onStart);
      window.removeEventListener('touchmove', onMove);
      window.removeEventListener('touchend', onEnd);
      window.removeEventListener('touchcancel', onCancel);
    };
  }, []);
}
