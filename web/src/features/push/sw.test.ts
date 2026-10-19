// The service worker's push handling (src/sw/push.ts): it shows exactly the
// server-rendered title, ignores payloads that are not ours, keeps click
// targets same-origin and focuses an open window before opening a new one.
import { describe, expect, it, vi } from 'vitest';
import { installPush, notificationOptions, parsePush, safePath } from '../../sw/push';

describe('parsePush', () => {
  it('accepts the server message', () => {
    expect(parsePush('{"v":1,"title":"@bob mentioned you","tag":"n1","url":"/notifications"}')).toEqual({
      v: 1,
      title: '@bob mentioned you',
      tag: 'n1',
      url: '/notifications',
    });
    expect(parsePush('{"v":1,"title":"New activity","url":"/notifications"}')).toEqual({
      v: 1,
      title: 'New activity',
      url: '/notifications',
    });
  });

  it('ignores anything else', () => {
    for (const p of [null, '', 'hello', '[]', '{"v":2,"title":"x"}', '{"v":1}', '{"v":1,"title":""}', `{"v":1,"title":"${'x'.repeat(501)}"}`]) {
      expect(parsePush(p)).toBeNull();
    }
  });

  it('keeps click targets on this origin', () => {
    expect(safePath('/notifications')).toBe('/notifications');
    expect(safePath('/p/abc?x=1')).toBe('/p/abc?x=1');
    for (const u of ['//evil.example/x', 'https://evil.example', 'javascript:alert(1)', '/\\evil', 'notifications']) {
      expect(safePath(u)).toBe('/notifications');
    }
    expect(parsePush('{"v":1,"title":"x","url":"https://evil.example"}')?.url).toBe('/notifications');
  });

  it('passes the tag and the path to the notification', () => {
    expect(notificationOptions({ v: 1, title: 't', tag: 'g', url: '/notifications' })).toEqual({ tag: 'g', data: { url: '/notifications' } });
  });
});

type Handler = (e: unknown) => void;

function fakeScope(windows: { url: string; focus: () => Promise<void>; navigate: (u: string) => Promise<void> }[] = []) {
  const handlers = new Map<string, Handler>();
  const scope = {
    registration: { showNotification: vi.fn(() => Promise.resolve()) },
    clients: {
      matchAll: vi.fn(() => Promise.resolve(windows)),
      openWindow: vi.fn(() => Promise.resolve(null)),
    },
    location: { origin: 'https://pcc.example' },
    addEventListener: (type: string, fn: Handler) => {
      handlers.set(type, fn);
    },
  };
  installPush(scope as unknown as Parameters<typeof installPush>[0]);
  return { scope, handlers };
}

describe('installPush', () => {
  it('shows the received title and nothing for foreign payloads', async () => {
    const { scope, handlers } = fakeScope();
    const waits: Promise<unknown>[] = [];
    const ev = (text: string | null) => ({
      data: text === null ? null : { text: () => text },
      waitUntil: (p: Promise<unknown>) => waits.push(p),
    });
    handlers.get('push')?.(ev('{"v":1,"title":"[Ñêŵ àçŧîṽîŧý~~~~~]","tag":"activity","url":"/notifications"}'));
    handlers.get('push')?.(ev('not json'));
    handlers.get('push')?.(ev(null));
    await Promise.all(waits);
    expect(scope.registration.showNotification).toHaveBeenCalledTimes(1);
    expect(scope.registration.showNotification).toHaveBeenCalledWith('[Ñêŵ àçŧîṽîŧý~~~~~]', {
      tag: 'activity',
      data: { url: '/notifications' },
    });
    // An untrusted event's waitUntil throws; the notification still shows.
    handlers.get('push')?.({
      data: { text: () => '{"v":1,"title":"x","url":"/notifications"}' },
      waitUntil: () => {
        throw new DOMException('untrusted', 'InvalidStateError');
      },
    });
    expect(scope.registration.showNotification).toHaveBeenCalledTimes(2);
  });

  it('focuses an open window on click, or opens one', async () => {
    const focus = vi.fn(() => Promise.resolve());
    const navigate = vi.fn(() => Promise.resolve());
    const open = fakeScope([{ url: 'https://pcc.example/feed', focus, navigate }]);
    const close = vi.fn();
    let wait: Promise<unknown> = Promise.resolve();
    open.handlers.get('notificationclick')?.({
      notification: { close, data: { url: '/notifications' } },
      waitUntil: (p: Promise<unknown>) => (wait = p),
    });
    await wait;
    expect(close).toHaveBeenCalled();
    expect(focus).toHaveBeenCalled();
    expect(navigate).toHaveBeenCalledWith('https://pcc.example/notifications');

    const none = fakeScope();
    none.handlers.get('notificationclick')?.({
      notification: { close, data: { url: 'https://evil.example' } },
      waitUntil: (p: Promise<unknown>) => (wait = p),
    });
    await wait;
    expect(none.scope.clients.openWindow).toHaveBeenCalledWith('https://pcc.example/notifications');
  });
});
