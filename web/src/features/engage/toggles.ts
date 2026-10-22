// Toggle coalescing for like and save (rate limiting is elastic on the server,
// where turning a toggle back is free). A post's toggle is sent at once; clicks
// while its request is in flight only move the wanted state, so a burst sends
// at most one more request, and a burst that ends where the server already is
// sends nothing more. A short rate limit (retry_after_s ≤ 5) is waited out
// once instead of shown. Owner: P1-W08.
import { ApiError } from '../../api/errors';

// SETTLE_MS is how long a toggle waits for another click in the same task
// before it is sent. It stays 0: a later send would leave the screen ahead of
// the server for longer than the request itself.
export const SETTLE_MS = 0;

// MAX_WAIT_S is the longest rate-limit hint waited out quietly.
const MAX_WAIT_S = 5;

export interface ToggleHandlers<R> {
  // send asks the server for state on.
  send: (ref: string, on: boolean) => Promise<R>;
  // landed is called when the server reached the state the person last asked for.
  landed: (ref: string, on: boolean, result: R) => void;
  // failed is called with the state the server is still in.
  failed: (ref: string, confirmed: boolean, err: unknown) => void;
  // settled is called once nothing is pending for ref any more (after landed
  // or failed, or when the clicks cancelled out).
  settled?: (ref: string) => void;
}

interface Entry {
  confirmed: boolean; // what the server holds
  wanted: boolean; // what the person last asked for
  timer: ReturnType<typeof setTimeout> | undefined;
  busy: boolean;
}

export interface Toggler {
  // set records that the person wants on; current is the state they saw
  // before this click (used when nothing for ref is pending).
  set: (ref: string, on: boolean, current: boolean) => void;
  // reset forgets every pending toggle (tests).
  reset: () => void;
}

const sleep = (ms: number) =>
  new Promise<void>((r) => {
    setTimeout(r, ms);
  });

export function makeToggler<R>(h: ToggleHandlers<R>, settleMs: number = SETTLE_MS): Toggler {
  const entries = new Map<string, Entry>();

  const sendOnce = async (ref: string, on: boolean): Promise<R> => {
    try {
      return await h.send(ref, on);
    } catch (err) {
      const wait = err instanceof ApiError && err.code === 'rate_limited' ? err.retryAfterS : undefined;
      if (wait === undefined || wait > MAX_WAIT_S) throw err;
      await sleep(wait * 1000 + 100);
      return h.send(ref, on);
    }
  };

  const flush = async (ref: string): Promise<void> => {
    const e = entries.get(ref);
    if (!e) return;
    e.timer = undefined;
    while (e.wanted !== e.confirmed) {
      const target = e.wanted;
      e.busy = true;
      try {
        const res = await sendOnce(ref, target);
        e.confirmed = target;
        if (e.wanted === target) h.landed(ref, target, res);
      } catch (err) {
        entries.delete(ref);
        h.failed(ref, e.confirmed, err);
        h.settled?.(ref);
        return;
      } finally {
        e.busy = false;
      }
    }
    entries.delete(ref);
    h.settled?.(ref);
  };

  return {
    set(ref, on, current) {
      let e = entries.get(ref);
      if (!e) {
        e = { confirmed: current, wanted: on, timer: undefined, busy: false };
        entries.set(ref, e);
      }
      e.wanted = on;
      if (e.busy) return; // the running flush picks the new state up
      if (e.timer !== undefined) clearTimeout(e.timer);
      e.timer = setTimeout(() => void flush(ref), settleMs);
    },
    reset() {
      entries.forEach((e) => {
        if (e.timer !== undefined) clearTimeout(e.timer);
      });
      entries.clear();
    },
  };
}
