// View-models (SCAFFOLD contract; plan §6.2). The data layer maps API JSON to
// these once; every format (Cards, Compact, Explorer, custom layouts) renders
// from them and never fetches its own data, so formats cannot differ in what
// they reveal (ARCHITECTURE §15.1). Additive changes only, via the steward.

export type Ref = string & { readonly __ref: unique symbol };

export const asRef = (s: string): Ref => s as Ref;

export type Preset = 'latest' | 'balanced' | 'top';
export type Relation = 'self' | 'friend' | 'friend_plus' | 'connected';
export type Audience = 'connected' | 'd1' | 'd2' | 'd3' | 'friend_plus';
export type RefPolicy = 'anyone' | 'two_hops' | 'friends' | 'nobody';
export type TreeLabel =
  | 'earlier'
  | 'replying_to'
  | 'this_post'
  | 'replies'
  | 'replies_to_replies'
  | 'generations_down'
  | 'generations_up';

// MarkdownDoc is the parsed post body (parsed once in the data layer by
// src/markdown; its node structure is owned by P0-W09).
export interface MarkdownDoc {
  readonly source: string;
  readonly nodes: readonly unknown[];
}

export interface HopVM {
  ref: Ref;
  handle: string;
}
export interface ChainVM {
  dist: number;
  firstHop?: HopVM;
  middle: HopVM[];
  hiddenMiddle: number;
  firstHopPlus: boolean;
}
export interface InviterVM {
  kind: 'user' | 'genesis' | 'deleted';
  handle?: string;
}
export interface AuthorVM {
  ref: Ref;
  handle: string;
  displayName: string;
  inviter: InviterVM;
  chain: ChainVM;
}
export interface ContextVM {
  ref: Ref;
  kind: 'ref' | 'cont';
  authorHandle: string;
  excerpt: string;
}
export interface PreviewVM {
  url: string;
  host: string;
  hostUnicode: string;
  lookalike: boolean;
  title?: string;
  description?: string;
  image?: MediaVM;
}
export interface LikesVM {
  inNetwork: number;
  names: HopVM[];
  others: number;
  byMe: boolean;
}
export interface MediaVM {
  ref: Ref;
  w: number;
  h: number;
  alt?: string;
}
export interface PostVM {
  ref: Ref;
  author: AuthorVM;
  tier: number;
  body: MarkdownDoc | null; // null when the body was not loaded (tree overflow)
  lang: string;
  createdAt: Date;
  editedAt?: Date;
  audience: Audience;
  audienceForced: boolean;
  refPolicy: RefPolicy;
  canReference: boolean;
  mine: boolean;
  context: ContextVM[];
  thread?: { index: number; total: number };
  previews: PreviewVM[];
  likes: LikesVM;
  savedByMe: boolean;
  replyCount: number;
  media: MediaVM[];
  fold: 'none' | 'card' | 'tree';
}
export type FeedItemVM =
  | { type: 'tier_start'; tier: number }
  | { type: 'tier_end'; tier: number }
  | { type: 'caught_up' }
  | { type: 'post'; post: PostVM }
  | { type: 'thread'; parts: PostVM[] }
  | { type: 'collapsed'; tier: number; count: number; token: string };
export interface FeedPageVM {
  session: string;
  preset: Preset;
  items: FeedItemVM[];
  next?: string;
}
export interface TreeItemVM {
  post: PostVM;
  tierDivider: boolean;
  shownAbove: boolean;
  alsoRepliesTo: HopVM[];
  alsoParents: Ref[];
}
export interface TreeRowVM {
  gen: number;
  label: TreeLabel;
  center: Ref;
  items: TreeItemVM[];
  more: number;
  cursor?: string;
}
export interface TreeVM {
  snapshot: string;
  focal: Ref;
  rows: TreeRowVM[];
  hasEarlier: boolean;
  hasLater: boolean;
}
export interface PersonVM {
  ref: Ref;
  handle: string;
  displayName: string;
  inviter: InviterVM;
  relation: Relation;
  request?: 'outgoing' | 'incoming';
  chain: ChainVM;
  blocked: boolean;
  muted: boolean;
}
