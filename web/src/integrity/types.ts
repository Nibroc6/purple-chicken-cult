// Props of the seven integrity components (SCAFFOLD contract; ARCHITECTURE §15.5,
// plan §6.2). Every format, theme and custom layout must render all seven, visibly.
import type { ChainVM, InviterVM, PostVM, Ref } from '../vm/types';

export type ReportTarget = { kind: 'post' | 'person' | 'inviter' | 'preview'; ref: Ref };

export interface TierHeaderProps {
  tier: number;
  kind: 'start' | 'end' | 'caught_up';
}
export interface ChainProps {
  chain: ChainVM;
  author: { ref: Ref; handle: string };
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
