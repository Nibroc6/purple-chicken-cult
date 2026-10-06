// View-models and pure helpers of the people feature. Owner: P0-W03 → P1-W03.
import { ApiError, errorKey } from '../../api/errors';
import type { components } from '../../api/schema.gen';
import { asRef, type ChainVM, type PersonVM } from '../../vm/types';
import type { PersonAction } from './api';

type PersonView = components['schemas']['PersonView'];
type ChainView = components['schemas']['ChainView'];

export function toChainVM(c: ChainView): ChainVM {
  return {
    dist: c.dist,
    ...(c.first_hop ? { firstHop: { ref: asRef(c.first_hop.ref), handle: c.first_hop.handle } } : {}),
    middle: (c.middle ?? []).map((h) => ({ ref: asRef(h.ref), handle: h.handle })),
    hiddenMiddle: c.hidden_middle,
    firstHopPlus: c.first_hop_plus,
  };
}

export function toPersonVM(p: PersonView): PersonVM {
  return {
    ref: asRef(p.ref),
    handle: p.handle,
    displayName: p.display_name,
    inviter: { kind: p.inviter.kind, ...(p.inviter.handle ? { handle: p.inviter.handle } : {}) },
    relation: p.relation,
    ...(p.request ? { request: p.request } : {}),
    chain: toChainVM(p.chain),
    blocked: p.blocked === true,
    muted: p.muted === true,
  };
}

// primaryActions are the friendship actions offered for p, in display order.
// A declined request reads "outgoing" on the server, so nothing here can tell
// the sender it was declined.
export function primaryActions(p: PersonVM): PersonAction[] {
  if (p.relation === 'self') return [];
  if (p.blocked) return ['unblock'];
  if (p.relation === 'friend' || p.relation === 'friend_plus') return ['unfriend'];
  if (p.request === 'incoming') return ['accept', 'decline'];
  if (p.request === 'outgoing') return ['withdraw'];
  return ['request'];
}

// secondaryActions are mute and block (never offered for oneself).
export function secondaryActions(p: PersonVM): PersonAction[] {
  if (p.relation === 'self') return [];
  if (p.blocked) return p.muted ? ['unmute'] : [];
  return [p.muted ? 'unmute' : 'mute', 'block'];
}

// confirmActions ask before running.
export const confirmActions: ReadonlySet<PersonAction> = new Set(['unfriend', 'block']);

// The friends Problem codes localized in the people namespace.
// i18n-dynamic: people:errors.
const peopleCodes = new Set(['already_friends', 'friend_cap', 'self_not_allowed']);

// peopleErrorKey returns the catalog key for a friends API error.
export function peopleErrorKey(err: unknown): string {
  if (err instanceof ApiError && peopleCodes.has(err.code)) return `people:errors.${err.code}`;
  return errorKey(err);
}

// errorParams are the ICU arguments of an error message.
export function errorParams(err: unknown): Record<string, number> {
  return err instanceof ApiError && err.retryAfterS !== undefined ? { seconds: err.retryAfterS } : {};
}

// reportPath is where the report flow (moderation) opens for a target.
export function reportPath(kind: 'person' | 'inviter', ref: string): string {
  return `/report?${new URLSearchParams({ kind, ref }).toString()}`;
}
