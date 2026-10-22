// Props of the seven integrity components (SCAFFOLD contract; ARCHITECTURE §15.5,
// plan §6.2). Every format, theme and custom layout must render all seven, visibly.
import { createContext } from 'react';
import type { ChainVM, InviterVM, PostVM, Preset, Ref } from '../vm/types';

export type ReportTarget = { kind: 'post' | 'person' | 'inviter' | 'preview'; ref: Ref };

export interface TierHeaderProps {
  tier: number;
  kind: 'start' | 'end' | 'caught_up';
}
export interface ChainProps {
  chain: ChainVM;
  author: { ref: Ref; handle: string };
  // post: given, the chain opens the "how you're connected" modal for it.
  post?: Ref;
}
export interface InviterLabelProps {
  inviter: InviterVM;
  invitee: Ref; // "report inviter" routes through the invitee (§3)
  onReport: (target: ReportTarget) => void;
}
export interface WhyButtonProps {
  post: Ref; // fetches /posts/{ref}/why lazily through an injected loader
}
export interface AudienceBadgeProps {
  audience: PostVM['audience'];
  forced?: boolean; // own posts only
}
export interface EditedMarkProps {
  editedAt?: Date;
}
export interface ReportActionProps {
  target: ReportTarget;
  onReport: (target: ReportTarget) => void;
}

// "Why am I seeing this?" (§5.3): GET /posts/{ref}/why mapped by the owning
// feature. Steward: contract request p0-w07-2.
export type WhyReason =
  | 'network_likes'
  | 'below_author_usual'
  | 'reply_penalty'
  | 'good_replies'
  | 'cold_start'
  | 'show_less'
  | 'chronological';
export interface WhyVM {
  tier: number;
  via: ChainVM;
  postedAt: Date;
  preset: Preset;
  shiftMinutes: number; // how far ranking moved the post; positive = up
  reasons: WhyReason[];
}
export type WhyLoader = (post: Ref) => Promise<WhyVM>;
// Provided by the page that owns the posts (feed, tree); WhyButton reads it.
// Undefined outside such a page (the button then has nothing to load).
export const WhyLoaderContext = createContext<WhyLoader | undefined>(undefined);

// Test ids every format must render (Vitest presence suite, Playwright visibility).
export const integrityTestIds = [
  'integrity-tier-header',
  'integrity-chain',
  'integrity-inviter-label',
  'integrity-why',
  'integrity-audience',
  'integrity-edited',
  'integrity-report',
] as const;

// A post card renders all of them but the inviter label: the author's
// inviter is one tap away, in the "how you're connected" modal its chain
// opens. Person cards and pages still show the label inline.
export const postIntegrityTestIds = integrityTestIds.filter((t) => t !== 'integrity-inviter-label');
