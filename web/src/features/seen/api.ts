// Seen API calls (plan §5.3 engageapi). Owner: P0-W06 → P1-W08.
import { api, unwrap } from '../../api/client';
import { ApiError } from '../../api/errors';
import type { PostVM } from '../../vm/types';
import { toPostVM } from '../post/vm';

export const recentKey = ['seen', 'recent'] as const;

export interface SeenPage {
  items: PostVM[];
  next?: string;
}

// postSeen records refs as seen (the server takes ≤ 50 and always answers 204).
// keepalive lets the request outlive the page (flush on hide).
export async function postSeen(refs: string[], keepalive = false): Promise<void> {
  const res = await api.POST('/seen', { body: { posts: refs }, ...(keepalive ? { keepalive: true } : {}) });
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

export async function fetchRecent(cursor: string | undefined): Promise<SeenPage> {
  const page = unwrap(await api.GET('/seen/recent', { params: { query: cursor ? { cursor } : {} } }));
  return { items: page.items.map(toPostVM), ...(page.next ? { next: page.next } : {}) };
}
