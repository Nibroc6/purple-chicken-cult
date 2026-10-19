// Push settings: the fail-closed states (unsupported browser, push disabled on
// the server, blocked permission), turning push on and off through the device
// seam, the hide-content option, the server's state winning over the browser's
// (logout or a moved endpoint shows "off" and re-subscribes on "turn on"), and
// localized errors.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/errors';
import { makeQueryClient } from '../../app/App';
import type { DeviceSubscription } from './api';
import { keyBytes, PushUnavailable, setDevicePush, type DevicePush } from './device';
import PushSettingsPage, { pushErrorKey } from './PushSettingsPage';

function json(status: number, body: unknown): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

interface Server {
  enabled: boolean;
  subs: Map<string, boolean>; // endpoint → hide_content
  posts: { endpoint: string; hide: boolean }[];
  deletes: string[];
  failPost?: Response;
}

function stubServer(over: Partial<Server> = {}): Server {
  const srv: Server = { enabled: true, subs: new Map(), posts: [], deletes: [], ...over };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      const path = u.pathname.replace(/^\/api/, '');
      if (path === '/session') return json(200, { authenticated: true, kind: 'user', csrf: 'x' });
      if (path === '/push/key') return json(200, srv.enabled ? { enabled: true, key: 'BAAA' } : { enabled: false });
      if (path === '/push/subscriptions' && req.method === 'GET') {
        const ep = u.searchParams.get('endpoint') ?? '';
        return json(200, { subscribed: srv.subs.has(ep), hide_content: srv.subs.get(ep) ?? false });
      }
      if (path === '/push/subscriptions' && req.method === 'POST') {
        if (srv.failPost) return srv.failPost;
        const b = (await req.json()) as { endpoint: string; hide_content: boolean };
        srv.posts.push({ endpoint: b.endpoint, hide: b.hide_content });
        srv.subs.set(b.endpoint, b.hide_content);
        return json(204, undefined);
      }
      if (path === '/push/subscriptions' && req.method === 'DELETE') {
        const b = (await req.json()) as { endpoint: string };
        srv.deletes.push(b.endpoint);
        srv.subs.delete(b.endpoint);
        return json(204, undefined);
      }
      return json(404, { error: 'not_found' });
    }),
  );
  return srv;
}

const devSub: DeviceSubscription = { endpoint: 'https://push.example/ep1', keys: { p256dh: 'p', auth: 'a' } };

function fakeDevice(over: Partial<DevicePush> & { have?: DeviceSubscription | null; grant?: NotificationPermission } = {}) {
  let have = over.have ?? null;
  let perm: NotificationPermission = 'default';
  const d = {
    subscribed: [] as string[],
    unsubscribed: 0,
    supported: () => true,
    permission: () => perm,
    requestPermission: () => {
      perm = over.grant ?? 'granted';
      return Promise.resolve(perm);
    },
    current: () => Promise.resolve(have),
    subscribe: (key: string) => {
      d.subscribed.push(key);
      have = devSub;
      return Promise.resolve(devSub);
    },
    unsubscribe: () => {
      d.unsubscribed++;
      have = null;
      return Promise.resolve();
    },
    ...over,
  };
  setDevicePush(d);
  return d;
}

function renderPage() {
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <MemoryRouter>
        <PushSettingsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  setDevicePush(null);
  vi.unstubAllGlobals();
});

describe('PushSettingsPage', () => {
  it('says so when the browser cannot do push', async () => {
    stubServer();
    fakeDevice({ supported: () => false });
    renderPage();
    expect(await screen.findByTestId('push-unsupported')).toBeTruthy();
    expect(screen.queryByTestId('push-enable')).toBeNull();
  });

  it('fails closed when the server has push disabled', async () => {
    stubServer({ enabled: false });
    const d = fakeDevice();
    renderPage();
    expect(await screen.findByTestId('push-server-disabled')).toBeTruthy();
    expect(screen.queryByTestId('push-enable')).toBeNull();
    expect(d.subscribed).toEqual([]);
  });

  it('turns push on with the server key, then off again', async () => {
    const srv = stubServer();
    const d = fakeDevice();
    renderPage();
    const status = await screen.findByTestId('push-status');
    expect(screen.getByTestId('push-settings').dataset.subscribed).toBe('false');
    expect(status.textContent).toBe('Off for this device.');
    fireEvent.click(screen.getByTestId('push-enable'));
    await waitFor(() => {
      expect(screen.getByTestId('push-settings').dataset.subscribed).toBe('true');
    });
    expect(d.subscribed).toEqual(['BAAA']);
    expect(srv.posts).toEqual([{ endpoint: devSub.endpoint, hide: false }]);
    expect(screen.getByTestId('push-status').textContent).toBe('On for this device.');
    fireEvent.click(screen.getByTestId('push-disable'));
    await waitFor(() => {
      expect(screen.getByTestId('push-settings').dataset.subscribed).toBe('false');
    });
    expect(srv.deletes).toEqual([devSub.endpoint]);
    expect(d.unsubscribed).toBe(1);
  });

  it('saves the hide-content option with the subscription', async () => {
    const srv = stubServer();
    srv.subs.set(devSub.endpoint, false);
    fakeDevice({ have: devSub });
    renderPage();
    const box = await screen.findByTestId<HTMLInputElement>('push-hide');
    expect(box.checked).toBe(false);
    fireEvent.click(box);
    await waitFor(() => {
      expect(srv.posts).toEqual([{ endpoint: devSub.endpoint, hide: true }]);
    });
    await waitFor(() => {
      expect(screen.getByTestId<HTMLInputElement>('push-hide').checked).toBe(true);
    });
  });

  it('shows off when the server dropped the browser subscription, and turning on re-sends it', async () => {
    const srv = stubServer(); // e.g. after logout: the browser still holds ep1
    const d = fakeDevice({ have: devSub });
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId('push-settings').dataset.subscribed).toBe('false');
    });
    fireEvent.click(screen.getByTestId('push-enable'));
    await waitFor(() => {
      expect(screen.getByTestId('push-settings').dataset.subscribed).toBe('true');
    });
    expect(d.subscribed).toEqual([]); // the existing subscription is reused
    expect(srv.posts).toEqual([{ endpoint: devSub.endpoint, hide: false }]);
  });

  it('respects a refused permission', async () => {
    const srv = stubServer();
    fakeDevice({ grant: 'denied' });
    renderPage();
    fireEvent.click(await screen.findByTestId('push-enable'));
    expect(await screen.findByTestId('push-denied')).toBeTruthy();
    expect(srv.posts).toEqual([]);
    expect(screen.getByTestId<HTMLButtonElement>('push-enable').disabled).toBe(true);
  });

  it('localizes server and device errors', async () => {
    stubServer({ failPost: json(422, { error: 'push_endpoint_invalid', field: 'endpoint' }) });
    fakeDevice();
    renderPage();
    fireEvent.click(await screen.findByTestId('push-enable'));
    expect((await screen.findByTestId('push-error')).textContent).toBe("This browser's push service is not supported.");
    cleanupAndUnavailable();
  });
});

function cleanupAndUnavailable() {
  expect(pushErrorKey(new PushUnavailable())).toBe('push:errors.push_unavailable');
  expect(pushErrorKey(new ApiError(409, { error: 'feature_disabled', feature: 'push' }))).toBe('push:errors.feature_disabled');
  expect(pushErrorKey(new ApiError(404, { error: 'not_found' }))).toBe('errors:not_found');
}

describe('keyBytes', () => {
  it('decodes base64url without padding', () => {
    expect(Array.from(keyBytes('AQID_-8'))).toEqual([1, 2, 3, 255, 239]);
  });
});
