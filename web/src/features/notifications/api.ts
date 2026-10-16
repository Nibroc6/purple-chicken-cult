// Notification API calls (plan §5.3 notifyapi). Owner: P0-W10 → P1-W05.
import { api, unwrap } from '../../api/client';
import { toNotificationPage, type NotificationPage } from './vm';

export async function fetchNotifications(cursor: string | undefined): Promise<NotificationPage> {
  return toNotificationPage(unwrap(await api.GET('/notifications', { params: { query: cursor ? { cursor } : {} } })));
}

export async function markRead(upTo: string): Promise<void> {
  unwrap(await api.POST('/notifications/read', { body: { up_to: upTo } }));
}

export async function setPostMute(post: string, on: boolean): Promise<void> {
  const params = { params: { path: { ref: post } } };
  unwrap(on ? await api.PUT('/posts/{ref}/notify-mute', params) : await api.DELETE('/posts/{ref}/notify-mute', params));
}
