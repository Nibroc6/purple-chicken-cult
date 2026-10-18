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

// PeopleVM is a person with the Phase 1 facts the people pages show (a
// PersonVM to everyone else).
export interface PeopleVM extends PersonVM {
  hiddenVia: boolean;
}

export function toPersonVM(p: PersonView): PeopleVM {
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
    hiddenVia: p.hidden_via === true,
  };
}

// hiddenVia reads the hide-via flag of a person (false for a plain PersonVM).
export function hiddenVia(p: PersonVM): boolean {
  return 'hiddenVia' in p && (p as PeopleVM).hiddenVia;
}

export function isFriend(p: PersonVM): boolean {
  return p.relation === 'friend' || p.relation === 'friend_plus';
}

// primaryActions are the friendship actions offered for p, in display order.
// A declined request reads "outgoing" on the server, so nothing here can tell
// the sender it was declined.
export function primaryActions(p: PersonVM): PersonAction[] {
  if (p.relation === 'self') return [];
  if (p.blocked) return ['unblock'];
  if (isFriend(p)) return ['unfriend'];
  if (p.request === 'incoming') return ['accept', 'decline'];
  if (p.request === 'outgoing') return ['withdraw'];
  return ['request'];
}

// secondaryActions are mute, hide-via (friends; showing the paths again for
// anyone hidden) and block (never offered for oneself).
export function secondaryActions(p: PersonVM): PersonAction[] {
  if (p.relation === 'self') return [];
  const via: PersonAction[] = hiddenVia(p) ? ['show_via'] : isFriend(p) && !p.blocked ? ['hide_via'] : [];
  if (p.blocked) return [...(p.muted ? (['unmute'] as const) : []), ...via];
  return [p.muted ? 'unmute' : 'mute', ...via, 'block'];
}

// canUpgrade: a plain friend can become friend+ in person (the person page).
export function canUpgrade(p: PersonVM): boolean {
  return p.relation === 'friend' && !p.blocked;
}

// confirmActions ask before running.
export const confirmActions: ReadonlySet<PersonAction> = new Set(['unfriend', 'block', 'hide_via']);

// cosignPurpose is the co-sign purpose of a graph-changing action (P1): a
// passkey session signs it in the co-sign dialog. Blocking a friend signs
// the Revocation the block writes.
export function cosignPurpose(action: PersonAction, p: PersonVM): string | undefined {
  switch (action) {
    case 'request':
      return 'friend_request';
    case 'accept':
      return 'friend_accept';
    case 'unfriend':
      return 'unfriend';
    case 'block':
      return isFriend(p) ? 'unfriend' : 'block';
    default:
      return undefined;
  }
}

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
