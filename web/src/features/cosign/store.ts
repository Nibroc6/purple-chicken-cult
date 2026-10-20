// Co-sign requests shared by the global dialog and the features whose writes
// need a passkey assertion (ARCHITECTURE §3 Phase 1, §17.4): friend requests and
// accepts, unfriend, block, invites, passkey changes, account deletion,
// recovery approval and two-person moderation. One request at a time.
// Owner: P1-W01.
import { useSyncExternalStore } from 'react';
import type { CosignPurpose } from './api';

export interface CosignRequest {
  purpose: CosignPurpose;
  target?: string;
}

// CosignResult goes into the body of the write it authorizes.
export interface CosignResult {
  intent: string;
  assertion: Record<string, unknown>;
}

interface Pending {
  req: CosignRequest;
  promise: Promise<CosignResult | null>;
  resolve: (r: CosignResult | null) => void;
}

let pending: Pending | null = null;
const listeners = new Set<() => void>();

function emit(): void {
  listeners.forEach((fn) => {
    fn();
  });
}

// requestCosign opens the co-sign dialog for purpose (and target, a ref) and
// resolves with the intent and assertion once the person signed, or null if
// they cancelled. A second request while one is open gets the open one's
// answer only if it asks for the same thing; otherwise it waits its turn.
export async function requestCosign(req: CosignRequest): Promise<CosignResult | null> {
  while (pending) {
    const cur = pending;
    if (cur.req.purpose === req.purpose && cur.req.target === req.target) return cur.promise;
    await cur.promise.catch(() => null);
  }
  let resolve: (r: CosignResult | null) => void = () => undefined;
  const promise = new Promise<CosignResult | null>((r) => {
    resolve = r;
  });
  pending = { req, promise, resolve };
  emit();
  return promise;
}

// settleCosign closes the dialog with the outcome.
export function settleCosign(r: CosignResult | null): void {
  const p = pending;
  pending = null;
  emit();
  p?.resolve(r);
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

// useCosignRequest returns the open request, if any.
export function useCosignRequest(): CosignRequest | null {
  return useSyncExternalStore(
    subscribe,
    () => pending?.req ?? null,
    () => null,
  );
}
