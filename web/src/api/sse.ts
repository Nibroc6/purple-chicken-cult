// Server-sent events (/api/events). SCAFFOLD hand-off stub; owner P0-W10 → P1-W05:
// one EventSource per tab with backoff and typed subscriptions.
type Handler = (data: unknown) => void;
const handlers = new Map<string, Set<Handler>>();

// subscribe registers fn for events of type; it returns an unsubscribe function.
// eslint-disable-next-line @typescript-eslint/no-unnecessary-type-parameters -- typed contract (plan §6.7)
export function subscribe<T>(type: string, fn: (d: T) => void): () => void {
  let set = handlers.get(type);
  if (!set) {
    set = new Set();
    handlers.set(type, set);
  }
  const h = fn as Handler;
  set.add(h);
  return () => {
    set.delete(h);
  };
}

// dispatch delivers an event to subscribers (used by the EventSource owner and tests).
export function dispatch(type: string, data: unknown): void {
  handlers.get(type)?.forEach((fn) => {
    fn(data);
  });
}
