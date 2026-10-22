// Engagement API calls (plan §5.3 engageapi). Owner: P0-W06 → P1-W08.
import { api, unwrap } from '../../api/client';
import { ApiError } from '../../api/errors';
import type { LikesVM, PostVM } from '../../vm/types';
import { toPostVM } from '../post/vm';
import { toLikesVM } from './vm';

// likesKey prefixes every like-summary query (['likes', ref]).
export const likesKey = ['likes'] as const;

const at = (ref: string) => ({ params: { path: { ref } } });

function ok(res: { response: Response; error?: unknown }): void {
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

// setLike likes (on) or unlikes a post and returns its like summary afterwards.
export async function setLike(ref: string, on: boolean): Promise<LikesVM> {
  const res = on ? await api.PUT('/posts/{ref}/like', at(ref)) : await api.DELETE('/posts/{ref}/like', at(ref));
  return toLikesVM(unwrap(res).likes);
}

export async function fetchLikes(ref: string): Promise<LikesVM> {
  return toLikesVM(unwrap(await api.GET('/posts/{ref}/likes', at(ref))));
}

// setSave saves (on) or unsaves a post. Saves are private: only the saver
// ever sees saved_by_me.
export async function setSave(ref: string, on: boolean): Promise<void> {
  ok(on ? await api.PUT('/posts/{ref}/save', at(ref)) : await api.DELETE('/posts/{ref}/save', at(ref)));
}

export async function hidePost(ref: string): Promise<void> {
  ok(await api.POST('/posts/{ref}/hide', at(ref)));
}

export async function showLess(ref: string): Promise<void> {
  ok(await api.POST('/posts/{ref}/show-less', at(ref)));
}

// unhidePost undoes a hide or a show less (a show less also gives back the
// step it took off the author).
export async function unhidePost(ref: string): Promise<void> {
  ok(await api.DELETE('/posts/{ref}/hide', at(ref)));
}

// hiddenKey prefixes the hidden-posts and show-less lists.
export const hiddenKey = ['hidden'] as const;

export interface HiddenPageVM {
  items: PostVM[];
  next?: string;
}

export async function fetchHidden(cursor: string | undefined): Promise<HiddenPageVM> {
  const page = unwrap(await api.GET('/hidden', { params: { query: cursor ? { cursor } : {} } }));
  return { items: page.items.map(toPostVM), ...(page.next ? { next: page.next } : {}) };
}

export interface ShowLessVM {
  ref: string;
  handle: string;
  displayName: string;
  count: number;
}

export async function fetchShowLess(): Promise<ShowLessVM[]> {
  return unwrap(await api.GET('/show-less')).items.map((x) => ({
    ref: x.ref,
    handle: x.handle,
    displayName: x.display_name,
    count: x.count,
  }));
}

export async function resetShowLess(ref: string): Promise<void> {
  ok(await api.DELETE('/show-less/{ref}', at(ref)));
}
