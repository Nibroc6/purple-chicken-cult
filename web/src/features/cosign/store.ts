// Co-sign requests shared by the global dialog and the features whose changes
// need a passkey signature (ARCHITECTURE §3 Phase 1, §17.4). One request at a
// time: a new request cancels the pending one. Owner: P1-W01.
import { useSyncExternalStore } from 'react';

// CosignRequest is what a feature asks to have signed.
export interface CosignRequest {
  purpose: string;
  target?: string;
  // summary is an already translated one-line description of the change,
  // shown above the record.
  summary?: string;
}

// CosignResult goes into the change's own request as {intent, assertion}.
export interface CosignResult {
  intent: string;
  assertion: Record<string, unknown>;
}

interface Pending {
  req: CosignRequest;
  resolve: (r: CosignResult | null) => void;
}

let pending: Pending | null = null;
const listeners = new Set<() => void>();

function emit(): void {
  listeners.forEach((fn) => {
    fn();
  });
}

// requestCosign opens the co-sign dialog for req and resolves with the intent
// and the passkey assertion once the person signed, or null if they cancelled.
export function requestCosign(req: CosignRequest): Promise<CosignResult | null> {
  pending?.resolve(null);
  return new Promise((resolve) => {
    pending = { req, resolve };
    emit();
  });
}

// settleCosign closes the dialog with the outcome.
export function settleCosign(result: CosignResult | null): void {
  const p = pending;
  pending = null;
  emit();
  p?.resolve(result);
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

// usePendingCosign returns the request being signed, if any.
export function usePendingCosign(): CosignRequest | undefined {
  return useSyncExternalStore(
    subscribe,
    () => pending?.req,
    () => undefined,
  );
}
