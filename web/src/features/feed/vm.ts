// Feed view-models: the API's FeedPage / FeedItem / WhyView mapped once in the
// data layer (plan §6.2). Posts go through the shared toPostVM (P0-W05).
// Owner: P0-W07 → P1-W08.
import type { components } from '../../api/schema.gen';
import type { WhyVM } from '../../integrity/types';
import type { QueryClient } from '@tanstack/react-query';
import type { FeedItemVM, FeedPageVM, PostVM, Preset } from '../../vm/types';
import { toChainVM, toPostVM } from '../post/vm';

type S = components['schemas'];

// feedKey prefixes every feed query (['feed', …]).
export const feedKey = ['feed'] as const;

// ownPostsKey holds the reader's posts published during this visit (D16): the
// reader's own posts are never feed candidates, so the client shows what it
// just published on top of the feed, outside any snapshot or tier.
export const ownPostsKey = [...feedKey, 'own'] as const;
const MAX_OWN = 20;

// addOwnPosts records a publish (one post, or the parts of a thread), newest first.
export function addOwnPosts(qc: QueryClient, posts: PostVM[]): void {
  if (posts.length === 0) return;
  qc.setQueryDefaults(ownPostsKey, { gcTime: Infinity }); // kept for the whole visit
  const item: FeedItemVM = posts.length === 1 && posts[0] ? { type: 'post', post: posts[0] } : { type: 'thread', parts: posts };
  qc.setQueryData<FeedItemVM[]>(ownPostsKey, (old) => [item, ...(old ?? [])].slice(0, MAX_OWN));
}

// withOwnPosts puts the reader's just-published posts in front of the feed pages.
export function withOwnPosts(pages: FeedPageVM[], own: readonly FeedItemVM[]): FeedPageVM[] {
  const first = pages[0];
  if (own.length === 0 || !first) return pages;
  return [{ session: first.session, preset: first.preset, items: [...own] }, ...pages];
}

// The presets a reader can pick in Phase 0 (Top arrives with P1-W08).
export const presets: readonly Preset[] = ['latest', 'balanced'];

// toFeedItemVM maps one item; an item the client cannot render (an unknown
// type, a post item without its post) is dropped rather than guessed at.
export function toFeedItemVM(it: S['FeedItem']): FeedItemVM | undefined {
  switch (it.type) {
    case 'tier_start':
    case 'tier_end':
      return { type: it.type, tier: it.tier ?? 0 };
    case 'caught_up':
      return { type: 'caught_up' };
    case 'post':
      return it.post ? { type: 'post', post: toPostVM(it.post) } : undefined;
    case 'thread':
      return it.thread && it.thread.length > 0 ? { type: 'thread', parts: it.thread.map(toPostVM) } : undefined;
    case 'collapsed':
      return it.collapsed
        ? { type: 'collapsed', tier: it.tier ?? 0, count: it.collapsed.count, token: it.collapsed.token }
        : undefined;
  }
  return undefined;
}

export function toFeedItems(items: S['FeedItem'][]): FeedItemVM[] {
  return items.flatMap((it) => {
    const vm = toFeedItemVM(it);
    return vm ? [vm] : [];
  });
}

export function toFeedPageVM(p: S['FeedPage']): FeedPageVM {
  return { session: p.session, preset: p.preset, items: toFeedItems(p.items), ...(p.next ? { next: p.next } : {}) };
}

export function toWhyVM(w: S['WhyView']): WhyVM {
  return {
    tier: w.tier,
    via: toChainVM(w.via),
    postedAt: new Date(w.posted_at),
    preset: w.preset,
    shiftMinutes: w.shift_minutes,
    reasons: w.reasons,
  };
}

// expandInPages replaces the collapsed group token with its posts, in place,
// so every layout renders them where the group stood.
export function expandInPages(pages: FeedPageVM[], token: string, items: FeedItemVM[]): FeedPageVM[] {
  return pages.map((p) =>
    p.items.some((it) => it.type === 'collapsed' && it.token === token)
      ? { ...p, items: p.items.flatMap((it) => (it.type === 'collapsed' && it.token === token ? items : [it])) }
      : p,
  );
}

// countPosts is the number of posts (thread parts included once per card) on the pages.
export function countPosts(pages: FeedPageVM[]): number {
  return pages.reduce((n, p) => n + p.items.filter((it) => it.type === 'post' || it.type === 'thread').length, 0);
}
