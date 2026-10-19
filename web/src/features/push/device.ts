// The browser side of Web Push, behind a seam (tests swap it; the e2e spec
// stubs PushManager so no real push service is ever contacted). Owner: P1-W05.
import type { DeviceSubscription } from './api';

export interface DevicePush {
  supported(): boolean;
  permission(): NotificationPermission;
  requestPermission(): Promise<NotificationPermission>;
  current(): Promise<DeviceSubscription | null>;
  subscribe(applicationServerKey: string): Promise<DeviceSubscription>;
  unsubscribe(): Promise<void>;
}

// READY_MS bounds the wait for the service worker (it is registered in
// production builds only).
export const READY_MS = 5000;

export class PushUnavailable extends Error {
  constructor() {
    super('push_unavailable');
    this.name = 'PushUnavailable';
  }
}

export function keyBytes(b64url: string): Uint8Array<ArrayBuffer> {
  const s = b64url.replace(/-/g, '+').replace(/_/g, '/');
  const bin = atob(s + '='.repeat((4 - (s.length % 4)) % 4));
  const out = new Uint8Array(new ArrayBuffer(bin.length));
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function toDevice(s: PushSubscription): DeviceSubscription {
  const j = s.toJSON();
  return { endpoint: s.endpoint, keys: { p256dh: j.keys?.p256dh ?? '', auth: j.keys?.auth ?? '' } };
}

async function registration(): Promise<ServiceWorkerRegistration> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(() => {
      reject(new PushUnavailable());
    }, READY_MS);
  });
  try {
    return await Promise.race([navigator.serviceWorker.ready, timeout]);
  } finally {
    clearTimeout(timer);
  }
}

export const browserPush: DevicePush = {
  supported: () =>
    typeof window !== 'undefined' && 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window,
  permission: () => Notification.permission,
  requestPermission: () => Notification.requestPermission(),
  async current() {
    const s = await (await registration()).pushManager.getSubscription();
    return s ? toDevice(s) : null;
  },
  async subscribe(key) {
    const reg = await registration();
    const s = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(key) });
    return toDevice(s);
  },
  async unsubscribe() {
    const s = await (await registration()).pushManager.getSubscription();
    if (s) await s.unsubscribe();
  },
};

let device: DevicePush = browserPush;

export function getDevicePush(): DevicePush {
  return device;
}

// setDevicePush replaces the seam (tests); null restores the browser's.
export function setDevicePush(d: DevicePush | null): void {
  device = d ?? browserPush;
}
