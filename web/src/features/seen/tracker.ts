// The seen queue (§4.3): one per tab. Posts the tracker saw are batched and
// sent every 5 s while anything is pending, and right away when the page is
// hidden (keepalive, so the request survives navigation). A ref is sent once
// per tab; a failed batch is retried with the next one. Owner: P0-W06 → P1-W08.
import { postSeen } from './api';

export const FLUSH_MS = 5_000;
export const BATCH = 50; // the server's per-request cap

const pending = new Set<string>();
const sent = new Set<string>();
let timer: ReturnType<typeof setTimeout> | undefined;
let inflight: Promise<void> | undefined;

function schedule(): void {
  if (timer !== undefined || pending.size === 0) return;
  timer = setTimeout(() => {
    timer = undefined;
    void flushSeen();
  }, FLUSH_MS);
}

// queueSeen adds a post the viewer saw (≥ 50 % visible for ≥ 1 s).
export function queueSeen(ref: string): void {
  if (sent.has(ref) || pending.has(ref)) return;
  pending.add(ref);
  schedule();
}

// flushSeen sends everything pending now, in batches of BATCH.
export async function flushSeen(keepalive = false): Promise<void> {
  if (timer !== undefined) {
    clearTimeout(timer);
    timer = undefined;
  }
  if (inflight) await inflight;
  if (pending.size === 0) return;
  const refs = [...pending];
  pending.clear();
  refs.forEach((r) => sent.add(r));
  inflight = (async () => {
    for (let i = 0; i < refs.length; i += BATCH) {
      const chunk = refs.slice(i, i + BATCH);
      try {
        await postSeen(chunk, keepalive);
      } catch {
        chunk.forEach((r) => {
          sent.delete(r);
          pending.add(r);
        });
      }
    }
  })();
  try {
    await inflight;
  } finally {
    inflight = undefined;
    schedule();
  }
}

// pendingSeen lists the refs waiting to be sent (tests, debugging).
export function pendingSeen(): string[] {
  return [...pending];
}

// resetSeenQueue forgets everything (tests).
export function resetSeenQueue(): void {
  if (timer !== undefined) clearTimeout(timer);
  timer = undefined;
  inflight = undefined;
  pending.clear();
  sent.clear();
}
