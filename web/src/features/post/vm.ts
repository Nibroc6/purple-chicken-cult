// toPostVM maps the API PostView to the PostVM every format renders (shared by
// feed, tree and people). SCAFFOLD hand-off stub; owner P0-W05 → P1-W12.
import type { components } from '../../api/schema.gen';
import { parse } from '../../markdown';
import type { AuthorVM, ChainVM, HopVM, PostVM } from '../../vm/types';
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
    body: p.body === '' ? null : parse(p.body),
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
