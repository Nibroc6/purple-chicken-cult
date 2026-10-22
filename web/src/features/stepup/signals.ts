// Client automation signals (ARCHITECTURE §17.2(4), plan P1-W07): once a
// minute while the app is visible, and when the page is hidden or left, the
// app reports navigator.webdriver, a DevTools-protocol hint and the NUMBER of
// input events per minute — nothing else: no content, no keys, no pointer
// positions or movement. Input of any kind counts (keys, taps, wheel, text
// input), so keyboard-only and screen-reader use is input like any other. The
// server never blocks anyone on these; together with request patterns they can
// only ask for a passkey step-up. Owner: P1-W07.
import { api } from '../../api/client';

export interface SignalsReport {
  webdriver: boolean;
  cdp: boolean;
  inputs_per_min: number[];
  visible_ms: number;
}

// The input events counted (never their content or coordinates).
export const INPUT_EVENTS = ['keydown', 'pointerdown', 'wheel', 'touchstart', 'input'] as const;

export const REPORT_EVERY_MS = 60_000;
const MINUTE_MS = 60_000;
const MAX_MINUTES = 60;

// cdpHint reports globals that automation drivers leave in the page
// (chromedriver's cdc_ arrays, Playwright's bindings). A hint, not a verdict.
export function cdpHint(w: object = globalThis): boolean {
  const names = Object.getOwnPropertyNames(w);
  return names.some((n) => n.startsWith('cdc_') || n.startsWith('__playwright') || n === '__pwInitScripts');
}

interface Env {
  now: () => number;
  visible: () => boolean;
  webdriver: () => boolean;
  cdp: () => boolean;
  target: EventTarget;
  send: (r: SignalsReport) => Promise<void>;
  setInterval: (fn: () => void, ms: number) => unknown;
  clearInterval: (h: unknown) => void;
}

function defaultEnv(): Env {
  return {
    now: () => performance.now(),
    visible: () => document.visibilityState === 'visible',
    webdriver: () => navigator.webdriver,
    cdp: () => cdpHint(),
    target: globalThis,
    send: async (r) => {
      await api.POST('/signals', { body: r });
    },
    setInterval: (fn, ms) => globalThis.setInterval(fn, ms),
    clearInterval: (h) => {
      globalThis.clearInterval(h as number);
    },
  };
}

// startSignals starts counting and reporting; it returns the stop function.
export function startSignals(over: Partial<Env> = {}): () => void {
  const env = { ...defaultEnv(), ...over };
  let start = env.now(); // start of the current report period
  let counts: number[] = [0];
  let visibleMs = 0;
  let visibleSince = env.visible() ? env.now() : undefined;

  const minuteIndex = () => Math.min(MAX_MINUTES - 1, Math.floor((env.now() - start) / MINUTE_MS));
  const onInput = () => {
    const i = minuteIndex();
    while (counts.length <= i) counts.push(0);
    counts[i] = (counts[i] ?? 0) + 1;
  };
  const closeVisible = () => {
    if (visibleSince !== undefined) {
      visibleMs += env.now() - visibleSince;
      visibleSince = env.visible() ? env.now() : undefined;
    }
  };
  const flush = () => {
    closeVisible();
    const elapsed = env.now() - start;
    if (visibleMs <= 0 && counts.every((n) => n === 0)) return;
    const minutes = Math.max(1, Math.min(MAX_MINUTES, Math.ceil(elapsed / MINUTE_MS)));
    while (counts.length < minutes) counts.push(0);
    const report: SignalsReport = {
      webdriver: env.webdriver(),
      cdp: env.cdp(),
      inputs_per_min: counts.slice(0, minutes),
      visible_ms: Math.min(Math.round(visibleMs), MAX_MINUTES * MINUTE_MS),
    };
    start = env.now();
    counts = [0];
    visibleMs = 0;
    env.send(report).catch(() => undefined);
  };
  const onVisibility = () => {
    if (env.visible()) {
      visibleSince ??= env.now();
      return;
    }
    flush();
  };

  for (const t of INPUT_EVENTS) env.target.addEventListener(t, onInput, { capture: true, passive: true });
  env.target.addEventListener('visibilitychange', onVisibility);
  env.target.addEventListener('pagehide', flush);
  const timer = env.setInterval(() => {
    if (env.visible()) flush();
  }, REPORT_EVERY_MS);
  return () => {
    for (const t of INPUT_EVENTS) env.target.removeEventListener(t, onInput, { capture: true });
    env.target.removeEventListener('visibilitychange', onVisibility);
    env.target.removeEventListener('pagehide', flush);
    env.clearInterval(timer);
    flush();
  };
}
