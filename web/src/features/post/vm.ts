// toPostVM maps the API PostView to the PostVM every format renders (shared by
// feed, tree and people), plus the pure helpers of the post and composer
// features. Owner: P0-W05 → P1-W12.
import { ApiError, errorKey } from '../../api/errors';
import type { components } from '../../api/schema.gen';
import { parse } from '../../markdown';
import type { Audience, AuthorVM, ChainVM, HopVM, PostVM, RefPolicy } from '../../vm/types';
import { asRef } from '../../vm/types';

type S = components['schemas'];

const FOLD_CHARS = 400;
const FOLD_LINES = 6;

// codePoints counts Unicode code points (the unit of every length rule, plan §4.2 mdscan).
export function codePoints(s: string): number {
  let n = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c < 0xdc00 || c > 0xdfff || i === 0 || s.charCodeAt(i - 1) < 0xd800 || s.charCodeAt(i - 1) > 0xdbff) n++;
  }
  return n;
}

export function toHopVM(h: S['ChainHop']): HopVM {
  return { ref: asRef(h.ref), handle: h.handle };
}

export function toChainVM(c: S['ChainView']): ChainVM {
  return {
    dist: c.dist,
    ...(c.first_hop ? { firstHop: toHopVM(c.first_hop) } : {}),
    middle: (c.middle ?? []).map(toHopVM),
    hiddenMiddle: c.hidden_middle,
    firstHopPlus: c.first_hop_plus,
  };
}

export function toAuthorVM(a: S['AuthorView']): AuthorVM {
  return {
    ref: asRef(a.ref),
    handle: a.handle,
    displayName: a.display_name,
    inviter: { kind: a.inviter.kind, ...(a.inviter.handle ? { handle: a.inviter.handle } : {}) },
    chain: toChainVM(a.chain),
  };
}

export function toPostVM(p: S['PostView']): PostVM {
  const long = codePoints(p.body) > FOLD_CHARS || p.body.split('\n').length > FOLD_LINES;
  return {
    ref: asRef(p.ref),
    author: toAuthorVM(p.author),
    tier: p.tier,
    body: p.body === '' ? null : parse(p.body, p.mentions ?? []),
    lang: p.lang,
    createdAt: new Date(p.created_at),
    ...(p.edited_at ? { editedAt: new Date(p.edited_at) } : {}),
    audience: p.audience,
    audienceForced: p.audience_forced ?? false,
    refPolicy: p.ref_policy,
    canReference: p.can_reference,
    mine: p.mine,
    context: (p.context ?? []).map((c) => ({
      ref: asRef(c.ref),
      kind: c.kind,
      authorHandle: c.author_handle,
      excerpt: c.excerpt,
    })),
    ...(p.thread ? { thread: { index: p.thread.index, total: p.thread.total } } : {}),
    previews: (p.previews ?? []).map((v) => ({
      url: v.url,
      host: v.host,
      hostUnicode: v.host_unicode,
      lookalike: v.lookalike,
      ...(v.title ? { title: v.title } : {}),
      ...(v.description ? { description: v.description } : {}),
    })),
    likes: {
      inNetwork: p.likes.in_network,
      names: (p.likes.names ?? []).map(toHopVM),
      others: p.likes.others,
      byMe: p.likes.by_me,
    },
    savedByMe: p.saved_by_me ?? false,
    replyCount: p.reply_count,
    media: (p.media ?? []).map((m) => ({ ref: asRef(m.ref), w: m.w, h: m.h, ...(m.alt ? { alt: m.alt } : {}) })),
    fold: long ? 'card' : 'none',
  };
}

// ---- length rules (§10.1.5): code points, never UTF-16 units ----

export const COUNTER_FROM = 280;
export const SOFT_LIMIT = 500;
export const HARD_LIMIT = 20_000;
export const MAX_PARTS = 25;
export const MAX_PARENTS = 3;

// CounterState: hidden below 280, shown up to 500, amber above the soft limit,
// over above the hard cap (publishing is refused).
export type CounterState = 'hidden' | 'normal' | 'amber' | 'over';

export function counterState(n: number): CounterState {
  if (n > HARD_LIMIT) return 'over';
  if (n > SOFT_LIMIT) return 'amber';
  if (n >= COUNTER_FROM) return 'normal';
  return 'hidden';
}

export const audiences: readonly Audience[] = ['connected', 'd1', 'd2', 'd3'];
export const refPolicies: readonly RefPolicy[] = ['anyone', 'two_hops', 'friends', 'nobody'];

// hasAudienceLimit: replying to such a post hides the context from some of
// your readers (§10.1.3; a probation-forced post reads d1 to everyone else).
export function hasAudienceLimit(p: PostVM): boolean {
  return p.audience !== 'connected' || p.audienceForced;
}

// The posts Problem codes localized in the post namespace.
// i18n-dynamic: post:errors.
const postCodes = new Set([
  'body_empty',
  'body_too_long',
  'lang_invalid',
  'audience_invalid',
  'ref_policy_invalid',
  'too_many_parents',
  'duplicate_parent',
  'cont_multiple',
  'cont_not_own',
  'cont_taken',
  'ref_not_allowed',
  'ref_cap',
  'too_many_parts',
  'cont_not_detachable',
]);

// postErrorKey returns the catalog key for a posts API error.
export function postErrorKey(err: unknown): string {
  if (err instanceof ApiError && postCodes.has(err.code)) return `post:errors.${err.code}`;
  return errorKey(err);
}

// errorParams are the ICU arguments of an error message.
export function errorParams(err: unknown): Record<string, number> {
  return err instanceof ApiError && err.retryAfterS !== undefined ? { seconds: err.retryAfterS } : {};
}
