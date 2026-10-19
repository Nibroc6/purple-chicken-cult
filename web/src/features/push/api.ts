// Push API calls (plan §5.3 pushapi). Owner: P1-W05.
import { api, unwrap } from '../../api/client';
import type { components } from '../../api/schema.gen';

export type PushKey = components['schemas']['PushKeyOut'];
export type PushState = components['schemas']['PushState'];

export interface DeviceSubscription {
  endpoint: string;
  keys: { p256dh: string; auth: string };
}

export async function fetchKey(): Promise<PushKey> {
  return unwrap(await api.GET('/push/key'));
}

export async function fetchState(endpoint: string): Promise<PushState> {
  return unwrap(await api.GET('/push/subscriptions', { params: { query: { endpoint } } }));
}

export async function saveSubscription(sub: DeviceSubscription, hideContent: boolean): Promise<void> {
  unwrap(await api.POST('/push/subscriptions', { body: { endpoint: sub.endpoint, keys: sub.keys, hide_content: hideContent } }));
}

export async function removeSubscription(endpoint: string): Promise<void> {
  unwrap(await api.DELETE('/push/subscriptions', { body: { endpoint } }));
}
