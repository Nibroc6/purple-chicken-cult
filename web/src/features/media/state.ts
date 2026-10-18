// Shared upload state: once the server reports media as feature_disabled, every
// picker hides itself for the rest of the session (fail closed). Owner: P1-W04.
import { useSyncExternalStore } from 'react';

let disabled = false;
const listeners = new Set<() => void>();

export function setMediaDisabled(v: boolean): void {
  if (disabled === v) return;
  disabled = v;
  listeners.forEach((fn) => {
    fn();
  });
}

export function useMediaDisabled(): boolean {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    () => disabled,
  );
}
