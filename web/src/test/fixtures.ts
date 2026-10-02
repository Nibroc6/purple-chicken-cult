// View-model fixtures for format tests.
import type { FeedPageVM, PostVM } from '../vm/types';
import { asRef } from '../vm/types';

export function postVM(over: Partial<PostVM> = {}): PostVM {
  return {
    ref: asRef('p1'),
    author: {
      ref: asRef('u1'),
      handle: 'alice',
      displayName: 'Alice',
      inviter: { kind: 'user', handle: 'bob' },
      chain: { dist: 2, firstHop: { ref: asRef('u2'), handle: 'carol' }, middle: [], hiddenMiddle: 0, firstHopPlus: false },
    },
    tier: 2,
    body: { source: 'hello', nodes: ['hello'] },
    lang: 'en',
    createdAt: new Date('2026-01-01T00:00:00Z'),
    editedAt: new Date('2026-01-01T01:00:00Z'),
    audience: 'd2',
    audienceForced: false,
    refPolicy: 'anyone',
    canReference: true,
    mine: false,
    context: [],
    previews: [],
    likes: { inNetwork: 3, names: [], others: 3, byMe: false },
    savedByMe: false,
    replyCount: 0,
    media: [],
    fold: 'none',
    ...over,
  };
}

export function feedPageVM(): FeedPageVM {
  return {
    session: 's1',
    preset: 'balanced',
    items: [{ type: 'tier_start', tier: 2 }, { type: 'post', post: postVM() }, { type: 'tier_end', tier: 2 }, { type: 'caught_up' }],
  };
}
