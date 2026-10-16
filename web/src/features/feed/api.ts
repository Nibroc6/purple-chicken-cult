// Feed API calls (plan §5.3 feedapi). Owner: P0-W07 → P1-W08.
import { api, unwrap } from '../../api/client';
import type { WhyVM } from '../../integrity/types';
import type { FeedItemVM, FeedPageVM, Preset } from '../../vm/types';
import { toFeedItems, toFeedPageVM, toWhyVM } from './vm';

// fetchFeed starts a session (no cursor; preset undefined = the saved one) or
// continues one (cursor).
export async function fetchFeed(preset: Preset | undefined, cursor: string | undefined): Promise<FeedPageVM> {
  const query = cursor ? { cursor } : preset ? { preset } : {};
  return toFeedPageVM(unwrap(await api.GET('/feed', { params: { query } })));
}

export async function expandGroup(token: string): Promise<FeedItemVM[]> {
  return toFeedItems(unwrap(await api.GET('/feed/expand', { params: { query: { token } } })).items);
}

export interface NewAbove {
  count: number;
  tier: number;
}

export async function fetchNewAbove(session: string): Promise<NewAbove> {
  return unwrap(await api.GET('/feed/new', { params: { query: { session } } }));
}

export async function fetchWhy(ref: string): Promise<WhyVM> {
  return toWhyVM(unwrap(await api.GET('/posts/{ref}/why', { params: { path: { ref } } })));
}
