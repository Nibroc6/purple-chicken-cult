// Step-up state shared by the global modal and the features that need a fresh
// passkey verification (D27). One request at a time: every caller waiting for
// a step-up shares the same answer. Owner: P0-W01 → P1-W07.
import { useSyncExternalStore } from 'react';
import { ApiError } from '../../api/errors';

let pending: { promise: Promise<boolean>; resolve: (ok: boolean) => void } | null = null;
const listeners = new Set<() => void>();

function emit(): void {
  listeners.forEach((fn) => {
    fn();
  });
}

// requestStepUp opens the step-up modal (if not already open) and resolves
// true once the person verified with a passkey, false if they cancelled.
export function requestStepUp(): Promise<boolean> {
  if (pending) return pending.promise;
  let resolve: (ok: boolean) => void = () => undefined;
  const promise = new Promise<boolean>((r) => {
    resolve = r;
  });
  pending = { promise, resolve };
  emit();
  return promise;
}

// settleStepUp closes the modal with the outcome.
export function settleStepUp(ok: boolean): void {
  const p = pending;
  pending = null;
  emit();
  p?.resolve(ok);
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

// useStepUpOpen reports whether a step-up is being requested.
export function useStepUpOpen(): boolean {
  return useSyncExternalStore(
    subscribe,
    () => pending !== null,
    () => false,
  );
}

// withStepUp runs fn; if the server asks for a fresh step-up it waits for the
// modal and, once verified, runs fn again (once).
export async function withStepUp<T>(fn: () => Promise<T>): Promise<T> {
  try {
    return await fn();
  } catch (err) {
    if (err instanceof ApiError && err.isStepUp && (await requestStepUp())) return fn();
    throw err;
  }
}
