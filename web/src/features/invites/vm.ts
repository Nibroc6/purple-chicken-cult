// View-models and pure helpers of the invites feature. Owner: P0-W04 → P1-W02.
// i18n-dynamic: invites:errors.
// i18n-dynamic: invites:countdown.
import { ApiError, errorKey } from '../../api/errors';
import type { components } from '../../api/schema.gen';
import type { PersonVM } from '../../vm/types';
import { toPersonVM } from '../people/vm';

type BudgetView = components['schemas']['BudgetView'];
type InviteeView = components['schemas']['InviteeView'];
type FrameView = components['schemas']['FrameView'];
type StatusView = components['schemas']['InvitesStatus'];

export type HandshakeState = components['schemas']['HandshakeStatus']['state'];
export type HandshakeKind = StatusView['kind'];
export type HandshakeOutcome = NonNullable<StatusView['outcome']>;
export type RetryHint = NonNullable<StatusView['hint']>;

export interface BudgetVM {
  available: number;
  cap: number;
  nextAt?: Date;
  probation?: { until: Date; needOutsideFriends: number; friendPlus: boolean };
  pausedUntil?: Date;
  revoked: boolean;
  // attempts is the build-up limit on showing a code, apart from the invites
  // saved: every code shown takes one, whether or not someone joins.
  attempts?: { available: number; cap: number; nextAt?: Date };
}

export function toBudgetVM(b: BudgetView): BudgetVM {
  return {
    available: b.available,
    cap: b.cap,
    ...(b.next_at ? { nextAt: new Date(b.next_at) } : {}),
    ...(b.probation
      ? {
          probation: {
            until: new Date(b.probation.until),
            needOutsideFriends: b.probation.need_outside_friends,
            friendPlus: b.probation.friend_plus === true,
          },
        }
      : {}),
    ...(b.paused_until ? { pausedUntil: new Date(b.paused_until) } : {}),
    revoked: b.revoked === true,
    ...(b.attempts
      ? {
          attempts: {
            available: b.attempts.available,
            cap: b.attempts.cap,
            ...(b.attempts.next_at ? { nextAt: new Date(b.attempts.next_at) } : {}),
          },
        }
      : {}),
  };
}

// canOpen reports whether the inviter may show a code now.
export function canOpen(b: BudgetVM): boolean {
  return b.available > 0 && (b.attempts?.available ?? 1) > 0 && !b.probation && !b.pausedUntil && !b.revoked;
}

export interface InviteeVM {
  person: PersonVM;
  joinedAt: Date;
  onProbation: boolean;
  probationUntil?: Date;
}

export function toInviteeVM(v: InviteeView): InviteeVM {
  return {
    person: toPersonVM(v.person),
    joinedAt: new Date(v.joined_at),
    onProbation: v.on_probation,
    ...(v.on_probation && v.probation_until ? { probationUntil: new Date(v.probation_until) } : {}),
  };
}

export interface FrameVM {
  payload: string;
  validUntil: Date;
}

export function toFrameVM(f: FrameView): FrameVM {
  return { payload: f.payload, validUntil: new Date(f.valid_until) };
}

// StatusVM is either device's view of a handshake (plan §5.3 InvitesStatus).
export interface StatusVM {
  state: HandshakeState;
  sas?: string;
  kind: HandshakeKind;
  mode: 'qr' | 'qr_audio' | 'dual_qr';
  quiet: boolean;
  attempt: number;
  attemptsLeft: number;
  playing: boolean;
  outcome?: HandshakeOutcome;
  hint?: RetryHint;
  sign?: 'needed' | 'waiting';
  peerSigned: boolean;
  hs?: string;
  signupToken?: string;
}

export function toStatusVM(s: StatusView): StatusVM {
  return {
    state: s.state,
    ...(s.sas ? { sas: s.sas } : {}),
    kind: s.kind,
    mode: s.mode,
    quiet: s.quiet,
    attempt: s.attempt,
    attemptsLeft: s.attempts_left,
    playing: s.playing === true,
    ...(s.outcome ? { outcome: s.outcome } : {}),
    ...(s.hint ? { hint: s.hint } : {}),
    ...(s.sign ? { sign: s.sign } : {}),
    peerSigned: s.peer_signed === true,
    ...(s.hs ? { hs: s.hs } : {}),
    ...(s.signup_token ? { signupToken: s.signup_token } : {}),
  };
}

// live reports whether a handshake may still change.
export function isLive(st: HandshakeState): boolean {
  return st === 'open' || st === 'claimed' || st === 'confirmed';
}

// passed reports whether the latest attempt let the handshake continue.
export function passed(s: StatusVM): boolean {
  return s.outcome === 'verified' || s.outcome === 'accepted';
}

// Countdown is a remaining duration split for the countdown messages.
export interface Countdown {
  key: 'countdown.days' | 'countdown.hours' | 'countdown.minutes';
  values: { d: number; h: number; m: number; s: number };
}

// countdown splits the time from now to target (never negative) and picks the
// message with the two largest units.
export function countdown(target: Date, now: Date): Countdown {
  const total = Math.max(0, Math.ceil((target.getTime() - now.getTime()) / 1000));
  const d = Math.floor(total / 86400);
  const h = Math.floor((total % 86400) / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const key = d > 0 ? 'countdown.days' : h > 0 ? 'countdown.hours' : 'countdown.minutes';
  return { key, values: { d, h, m, s } };
}

// Codes with invites-specific wording (invites:errors.<code>); the rest use errors:<code>.
const codes = new Set(['no_invites', 'on_probation', 'invites_paused', 'friend_cap', 'mode_unsupported']);

export function invitesErrorKey(err: unknown): string {
  if (err instanceof ApiError && codes.has(err.code)) return `invites:errors.${err.code}`;
  return errorKey(err);
}

export function errorParams(err: unknown): Record<string, number> {
  if (err instanceof ApiError && err.retryAfterS !== undefined) return { seconds: err.retryAfterS };
  return {};
}

// joinURL is the address an invitee (path /join) or a recovering device
// (path /recover) opens to scan the code.
export function joinURL(origin: string, path: '/join' | '/recover' = '/join'): string {
  return `${new URL(origin).host}${path}`;
}
