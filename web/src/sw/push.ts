/// <reference lib="webworker" />
// Push handling in the service worker (P1-W05; plan §6.8). The server renders
// the text from its localized catalogs in the recipient's ui_locale (or only
// the generic "new activity" text when the subscription has hide_content), so
// the worker shows exactly what it receives: a title, a tag that lets a newer
// push replace an older one, and a same-origin path to open on click. Nothing
// is fetched, cached or stored. A payload that is not ours shows nothing.

export interface PushMessage {
  v: 1;
  title: string;
  tag?: string;
  url: string;
}

const MAX_TITLE = 500;

// parsePush validates a decrypted push payload.
export function parsePush(text: string | null | undefined): PushMessage | null {
  if (!text) return null;
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch {
    return null;
  }
  if (typeof raw !== 'object' || raw === null) return null;
  const m = raw as Record<string, unknown>;
  if (m.v !== 1 || typeof m.title !== 'string' || m.title === '' || m.title.length > MAX_TITLE) return null;
  const url = typeof m.url === 'string' ? safePath(m.url) : '/notifications';
  const out: PushMessage = { v: 1, title: m.title, url };
  if (typeof m.tag === 'string' && m.tag.length <= 200) out.tag = m.tag;
  return out;
}

// safePath keeps only same-origin absolute paths ("/notifications"); anything
// else ("//evil", "https://…", "javascript:") becomes /notifications.
export function safePath(u: string): string {
  return /^\/(?![/\\])[\w\-./?=&%]*$/.test(u) ? u : '/notifications';
}

export function notificationOptions(m: PushMessage): NotificationOptions {
  const o: NotificationOptions = { data: { url: m.url } };
  if (m.tag) o.tag = m.tag;
  return o;
}

interface Scope {
  registration: ServiceWorkerRegistration;
  clients: Clients;
  location: { origin: string };
  addEventListener(type: 'push', fn: (e: PushEvent) => void): void;
  addEventListener(type: 'notificationclick', fn: (e: NotificationEvent) => void): void;
}

function keepAlive(e: ExtendableEvent, p: Promise<unknown>): void {
  try {
    e.waitUntil(p);
  } catch {
    // Synthetic (untrusted) events cannot extend their lifetime; p still runs.
  }
}

// openPath focuses an open window of the app on path, or opens one.
export async function openPath(scope: Scope, path: string): Promise<void> {
  const target = new URL(safePath(path), scope.location.origin).href;
  const wins = await scope.clients.matchAll({ type: 'window', includeUncontrolled: true });
  for (const w of wins) {
    if (new URL(w.url).origin === scope.location.origin) {
      await w.focus();
      if (w.url !== target) await w.navigate(target);
      return;
    }
  }
  await scope.clients.openWindow(target);
}

// installPush registers the push and notificationclick handlers on scope.
export function installPush(scope: Scope): void {
  scope.addEventListener('push', (e) => {
    let text: string | null;
    try {
      text = e.data?.text() ?? null;
    } catch {
      text = null;
    }
    const m = parsePush(text);
    if (!m) return;
    keepAlive(e, scope.registration.showNotification(m.title, notificationOptions(m)));
  });
  scope.addEventListener('notificationclick', (e) => {
    e.notification.close();
    const data = e.notification.data as { url?: unknown } | null;
    keepAlive(e, openPath(scope, typeof data?.url === 'string' ? data.url : '/notifications'));
  });
}

declare const self: ServiceWorkerGlobalScope;

export const pushEnabled = typeof ServiceWorkerGlobalScope !== 'undefined' && self instanceof ServiceWorkerGlobalScope;

if (pushEnabled) installPush(self);
