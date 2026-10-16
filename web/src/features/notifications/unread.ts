// The unread count shared by the nav badge and the notifications page. Owner:
// P0-W10 → P1-W05. Fed by SSE hello/notification events and by list responses.
import { useEffect, useSyncExternalStore } from 'react';
import { subscribe as subscribeSSE } from '../../api/sse';

let unread: number | undefined;
const listeners = new Set<() => void>();

export function setUnread(n: number | undefined): void {
  if (n === unread) return;
  unread = n;
  listeners.forEach((fn) => {
    fn();
  });
}

export function getUnread(): number | undefined {
  return unread;
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

interface UnreadEvent {
  unread?: number;
}

// useUnread keeps the event stream open while mounted and returns the count.
export function useUnread(): number | undefined {
  useEffect(() => {
    const on = (d: UnreadEvent) => {
      if (typeof d.unread === 'number') setUnread(d.unread);
    };
    const offHello = subscribeSSE<UnreadEvent>('hello', on);
    const offNote = subscribeSSE<UnreadEvent>('notification', on);
    return () => {
      offHello();
      offNote();
    };
  }, []);
  return useSyncExternalStore(subscribe, getUnread, getUnread);
}

// resetUnread forgets the count (tests).
export function resetUnread(): void {
  unread = undefined;
}
