// The unread-messages count shared by the nav badge, the list and the
// conversation page. Owner: direct messages (contract request dm-1). Read from
// GET /messages/unread when the stream (re)connects, then kept live by the SSE
// message event, which carries the new total.
import { useEffect, useSyncExternalStore } from 'react';
import { subscribe as subscribeSSE } from '../../api/sse';
import { fetchUnread } from './api';

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

export interface LiveMessage {
  conversation?: string;
  unread?: number;
}

// refreshUnread re-reads the count (after reading or leaving a conversation).
export function refreshUnread(): void {
  fetchUnread().then(setUnread, () => undefined);
}

// useUnread keeps the event stream open while mounted and returns the count.
export function useUnread(): number | undefined {
  useEffect(() => {
    // hello: a (re)connected stream may have missed messages.
    const offHello = subscribeSSE('hello', refreshUnread);
    const offMessage = subscribeSSE<LiveMessage>('message', (d) => {
      if (typeof d.unread === 'number') setUnread(d.unread);
    });
    if (unread === undefined) refreshUnread();
    return () => {
      offHello();
      offMessage();
    };
  }, []);
  return useSyncExternalStore(subscribe, getUnread, getUnread);
}

// resetUnread forgets the count (tests).
export function resetUnread(): void {
  unread = undefined;
}
