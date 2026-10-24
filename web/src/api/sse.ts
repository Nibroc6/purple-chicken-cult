// Server-sent events (/api/events, plan §6.7). Owner: P0-W10 → P1-W05.
//
// One EventSource per tab, opened while at least one subscription exists and
// closed with the last one. On an error (network, server restart, the session
// ended) the stream is closed and reopened with exponential backoff and jitter
// (1 s doubling to 60 s, reset once a stream opens); the server sends hello
// {unread} first on every connection, so nothing is lost across reconnects.
// Event data is JSON; frames that fail to parse are ignored.
type Handler = (data: unknown) => void;

export const SSE_URL = '/api/events';
export const BACKOFF_BASE_MS = 1000;
export const BACKOFF_MAX_MS = 60_000;

const handlers = new Map<string, Set<Handler>>();
const errorListeners = new Set<() => void>();
let source: EventSource | undefined;
let listened = new Set<string>();
let timer: ReturnType<typeof setTimeout> | undefined;
let attempt = 0;

function count(): number {
  let n = 0;
  handlers.forEach((s) => (n += s.size));
  return n;
}

function listen(es: EventSource, type: string): void {
  if (listened.has(type)) return;
  listened.add(type);
  es.addEventListener(type, (e: MessageEvent<string>) => {
    let data: unknown;
    try {
      data = JSON.parse(e.data);
    } catch {
      return;
    }
    dispatch(type, data);
  });
}

// backoffDelay is the wait before reconnect attempt n (0-based): base·2ⁿ capped,
// times a jitter factor in [0.5, 1).
export function backoffDelay(n: number, rand: () => number = Math.random): number {
  const d = Math.min(BACKOFF_MAX_MS, BACKOFF_BASE_MS * 2 ** Math.min(n, 16));
  return Math.round(d * (0.5 + rand() / 2));
}

function connect(): void {
  timer = undefined;
  if (source || count() === 0 || typeof EventSource === 'undefined') return;
  const es = new EventSource(SSE_URL);
  source = es;
  listened = new Set();
  handlers.forEach((_, type) => {
    listen(es, type);
  });
  es.onopen = () => {
    attempt = 0;
  };
  es.onerror = () => {
    if (source !== es) return;
    errorListeners.forEach((fn) => {
      fn();
    });
    es.close();
    source = undefined;
    if (count() > 0 && timer === undefined) {
      timer = setTimeout(connect, backoffDelay(attempt));
      attempt++;
    }
  };
}

function disconnect(): void {
  if (timer !== undefined) clearTimeout(timer);
  timer = undefined;
  source?.close();
  source = undefined;
  attempt = 0;
}

// subscribe registers fn for events of type (opening the stream if needed); it
// returns an unsubscribe function (the last one closes the stream).
// eslint-disable-next-line @typescript-eslint/no-unnecessary-type-parameters -- typed contract (plan §6.7)
export function subscribe<T>(type: string, fn: (d: T) => void): () => void {
  let set = handlers.get(type);
  if (!set) {
    set = new Set();
    handlers.set(type, set);
  }
  const h = fn as Handler;
  set.add(h);
  if (source) listen(source, type);
  else if (timer === undefined) connect();
  return () => {
    set.delete(h);
    if (count() === 0) disconnect();
  };
}

// onStreamError subscribes to the stream failing (the session may have ended:
// EventSource cannot say why, so the session watch asks /api/session).
export function onStreamError(fn: () => void): () => void {
  errorListeners.add(fn);
  return () => errorListeners.delete(fn);
}

// dispatch delivers an event to subscribers (used by the EventSource owner and tests).
export function dispatch(type: string, data: unknown): void {
  handlers.get(type)?.forEach((fn) => {
    fn(data);
  });
}

// resetSSE closes the stream and drops every subscription (tests).
export function resetSSE(): void {
  disconnect();
  handlers.clear();
}
