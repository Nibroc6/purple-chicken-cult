// Feed view-models: the API's FeedPage / FeedItem / WhyView mapped once in the
// data layer (plan §6.2). Posts go through the shared toPostVM (P0-W05).
// Owner: P0-W07 → P1-W08.
import type { components } from '../../api/schema.gen';
import type { WhyVM } from '../../integrity/types';
import type { FeedItemVM, FeedPageVM, Preset } from '../../vm/types';
import { toChainVM, toPostVM } from '../post/vm';

type S = components['schemas'];

// feedKey prefixes every feed query (['feed', …]).
export const feedKey = ['feed'] as const;

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
