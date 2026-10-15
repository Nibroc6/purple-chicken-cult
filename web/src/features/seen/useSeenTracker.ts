// useSeenTracker returns a callback ref for the element that carries
// data-seen-ref (a card's own post, §10.1.2). Context rows and collapsed thread
// parts never carry the attribute, so they are never observed: an element
// without it is ignored. A post counts as seen after ≥ 50 % of it was visible
// for ≥ 1 s while the page itself was visible. Owner: P0-W06 → P1-W08.
import { useCallback, useEffect, useRef } from 'react';
import { queueSeen } from './tracker';

export const SEEN_RATIO = 0.5;
export const SEEN_MS = 1_000;

interface Tracker {
  io: IntersectionObserver | undefined;
  visible: Set<HTMLElement>;
  timers: Map<HTMLElement, ReturnType<typeof setTimeout>>;
}

export function useSeenTracker(onSeen: (ref: string) => void = queueSeen): (el: HTMLElement | null) => void {
  const seenCb = useRef(onSeen);
  useEffect(() => {
    seenCb.current = onSeen;
  }, [onSeen]);
  const tr = useRef<Tracker>({ io: undefined, visible: new Set(), timers: new Map() });

  const stop = useCallback((el: HTMLElement) => {
    const t = tr.current.timers.get(el);
    if (t !== undefined) clearTimeout(t);
    tr.current.timers.delete(el);
  }, []);

  const start = useCallback((el: HTMLElement) => {
    const s = tr.current;
    if (s.timers.has(el) || document.visibilityState === 'hidden') return;
    s.timers.set(
      el,
      setTimeout(() => {
        s.timers.delete(el);
        s.visible.delete(el);
        s.io?.unobserve(el);
        const ref = el.dataset.seenRef;
        if (ref) seenCb.current(ref);
      }, SEEN_MS),
    );
  }, []);

  const observer = useCallback((): IntersectionObserver | undefined => {
    const s = tr.current;
    if (s.io || typeof IntersectionObserver === 'undefined') return s.io;
    s.io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          const el = e.target as HTMLElement;
          if (e.isIntersecting && e.intersectionRatio >= SEEN_RATIO) {
            s.visible.add(el);
            start(el);
          } else {
            s.visible.delete(el);
            stop(el);
          }
        }
      },
      { threshold: [0, SEEN_RATIO, 1] },
    );
    return s.io;
  }, [start, stop]);

  // Hidden page: nothing is being seen. Back to visible: restart the clocks.
  useEffect(() => {
    const s = tr.current;
    const onVis = () => {
      if (document.visibilityState === 'hidden') {
        s.timers.forEach((_, el) => {
          stop(el);
        });
      } else {
        s.visible.forEach((el) => {
          start(el);
        });
      }
    };
    document.addEventListener('visibilitychange', onVis);
    return () => {
      document.removeEventListener('visibilitychange', onVis);
      s.timers.forEach((t) => {
        clearTimeout(t);
      });
      s.timers.clear();
      s.visible.clear();
      s.io?.disconnect();
      s.io = undefined;
    };
  }, [start, stop]);

  return useCallback(
    (el: HTMLElement | null) => {
      if (!el?.dataset.seenRef) return;
      const io = observer();
      if (!io) return;
      io.observe(el);
      return () => {
        io.unobserve(el);
        tr.current.visible.delete(el);
        stop(el);
      };
    },
    [observer, stop],
  );
}
