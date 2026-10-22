// Per-tab engagement state the layouts read while a request is in flight or
// after it landed: posts hidden in this session (hide / show less) and the
// last failed action per post. Owner: P0-W06 → P1-W08.
import { useSyncExternalStore } from 'react';

export type HiddenKind = 'hide' | 'show_less';

const hidden = new Map<string, HiddenKind>();
const failed = new Map<string, unknown>();
const listeners = new Set<() => void>();
let version = 0;

function emit(): void {
  version++;
  listeners.forEach((fn) => {
    fn();
  });
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

export function setHidden(ref: string, kind: HiddenKind | undefined): void {
  if (kind) hidden.set(ref, kind);
  else hidden.delete(ref);
  emit();
}

export function setFailed(ref: string, err: unknown): void {
  if (err === undefined) failed.delete(ref);
  else failed.set(ref, err);
  emit();
}

// hiddenKind is how the post was hidden in this session (outside React).
export function hiddenKind(ref: string): HiddenKind | undefined {
  return hidden.get(ref);
}

// useHidden is how the post was hidden in this session, if it was.
export function useHidden(ref: string): HiddenKind | undefined {
  useSyncExternalStore(subscribe, () => version);
  return hidden.get(ref);
}

// useFailed is the error of the last failed action on the post, if any.
export function useFailed(ref: string): unknown {
  useSyncExternalStore(subscribe, () => version);
  return failed.get(ref);
}

// resetEngageStore clears everything (tests).
export function resetEngageStore(): void {
  hidden.clear();
  failed.clear();
  emit();
}
