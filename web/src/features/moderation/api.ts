// Moderation API calls (plan §5.3 modapi). Owner: P0-W12 → P1-W06.
import { api, unwrap } from '../../api/client';
import { ApiError, errorKey } from '../../api/errors';
import type { components } from '../../api/schema.gen';

export type ModItem = components['schemas']['ModItem'];
export type BlameItem = components['schemas']['BlameItem'];
export type LogItem = components['schemas']['LogItem'];
export type ModPending = components['schemas']['ModPending'];
export type CSAMHold = components['schemas']['CSAMHoldView'];
export interface Page<T> {
  items: T[];
  next?: string;
}

export const targetKinds = ['post', 'person', 'inviter', 'preview'] as const;
export type TargetKind = (typeof targetKinds)[number];

// The closed reason codes (server CHECKs; plan §3.2). Members report with
// reportReasons; moderators resolve with modReasons (adds ban_evasion).
export const reportReasons = [
  'spam',
  'bot',
  'harassment',
  'impersonation',
  'misleading_preview',
  'illegal',
  'csam',
  'other',
] as const;
export type ReportReason = (typeof reportReasons)[number];
export const modReasons = [...reportReasons, 'ban_evasion'] as const;
export type ModReason = (typeof modReasons)[number];

export const actions = ['remove_post', 'suspend_user', 'dismiss'] as const;
export type Action = (typeof actions)[number];

// NoteMax bounds a report note in code points (server: 1,000).
export const NoteMax = 1000;

export function isTargetKind(s: string | null): s is TargetKind {
  return s !== null && (targetKinds as readonly string[]).includes(s);
}

// isRef reports whether s looks like an opaque API ref.
export function isRef(s: string | null): s is string {
  return s !== null && /^[A-Za-z0-9_-]{1,256}$/.test(s);
}

export function codePoints(s: string): number {
  return Array.from(s).length;
}

// The reason a report page preselects for a target kind.
export function defaultReason(kind: TargetKind): ReportReason {
  return kind === 'preview' ? 'misleading_preview' : kind === 'inviter' ? 'bot' : 'spam';
}

// sendReport files a report. The answer is the same 202 whether or not the
// target exists or is visible (the server drops invisible targets silently).
export async function sendReport(kind: TargetKind, ref: string, reason: ReportReason, note: string): Promise<void> {
  const body = note.trim() === '' ? { target: { kind, ref }, reason } : { target: { kind, ref }, reason, note: note.trim() };
  const res = await api.POST('/reports', { body });
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

export async function fetchBlame(cursor: string | undefined): Promise<Page<BlameItem>> {
  return unwrap(await api.GET('/blame', { params: { query: cursor ? { cursor } : {} } }));
}

export async function fetchQueue(cursor: string | undefined): Promise<Page<ModItem>> {
  return unwrap(await api.GET('/mod/queue', { params: { query: cursor ? { cursor } : {} } }));
}

export async function fetchLog(cursor: string | undefined): Promise<Page<LogItem>> {
  return unwrap(await api.GET('/mod/log', { params: { query: cursor ? { cursor } : {} } }));
}

export async function resolveItem(id: string, action: Action, reason: ModReason): Promise<void> {
  const res = await api.POST('/mod/items/{id}/resolve', { params: { path: { id } }, body: { action, reason } });
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

// Cosig is the second moderator's co-signature of the ModAction record (the
// co-sign dialog's result).
export interface Cosig {
  intent: string;
  assertion: Record<string, unknown>;
}

// confirmItem confirms the two-person action pending on an item; a passkey
// session sends its co-signature, a session without one (test hooks) nothing.
export async function confirmItem(id: string, cosig?: Cosig): Promise<void> {
  const res = await api.POST('/mod/items/{id}/confirm', { params: { path: { id } }, ...(cosig ? { body: cosig } : {}) });
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

export async function claimItem(id: string): Promise<void> {
  const res = await api.POST('/mod/items/{id}/claim', { params: { path: { id } } });
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

// startCSAMReport starts the two-person csam_report action with the
// CyberTipline report reference.
export async function startCSAMReport(id: string, reportRef: string): Promise<void> {
  const res = await api.POST('/mod/items/{id}/csam-report', { params: { path: { id } }, body: { report_ref: reportRef } });
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

// startQuarantine starts the two-person quarantine of the subtree the item's
// subject belongs to.
export async function startQuarantine(id: string): Promise<void> {
  const res = await api.POST('/mod/quarantine', { body: { subject_item: id } });
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

// sessionCosigns reports whether this session signed in with a passkey: its
// confirmations are then co-signed (a session without one — test hooks only —
// is not asked).
export async function sessionCosigns(): Promise<boolean> {
  try {
    return unwrap(await api.GET('/auth/passkeys')).items.some((p) => p.current);
  } catch {
    return false;
  }
}

// reportRefOK mirrors the server's CyberTipline reference rule.
export function reportRefOK(s: string): boolean {
  return /^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$/.test(s);
}

// The two-person actions a queue item can wait on.
export const pendingActions = ['suspend_user', 'dismiss', 'quarantine_subtree', 'csam_report'] as const;
export type PendingAction = (typeof pendingActions)[number];

// actionsFor lists the actions the resolve form offers for an item:
// remove_post only where there is a post.
export function actionsFor(it: ModItem): Action[] {
  const s = it.summary ?? {};
  const post = it.post !== undefined || (s.kind_post ?? 0) > 0 || (s.kind_preview ?? 0) > 0 || (it.source === 'csam' && (s.preview ?? 0) > 0);
  return actions.filter((a) => a !== 'remove_post' || post);
}

// canQuarantine: the subject's invite subtree has enough confirmed-bad accounts.
export function canQuarantine(it: ModItem): boolean {
  const n = it.summary?.subtree_confirmed;
  const k = it.summary?.quarantine_k;
  return n !== undefined && k !== undefined && k > 0 && n >= k;
}

// The moderation Problem codes localized in the moderation namespace.
// i18n-dynamic: moderation:errors.
const codes = new Set([
  'kind_invalid',
  'reason_invalid',
  'note_too_long',
  'action_invalid',
  'item_resolved',
  'action_pending',
  'post_gone',
  'no_subject',
  'second_moderator_required',
  'action_expired',
  'subject_inactive',
  'item_claimed',
  'quarantine_threshold',
  'quarantine_active',
  'csam_already_reported',
]);

export function moderationErrorKey(err: unknown): string {
  if (err instanceof ApiError && codes.has(err.code)) return `moderation:errors.${err.code}`;
  return errorKey(err);
}

export function errorParams(err: unknown): Record<string, number> {
  return err instanceof ApiError && err.retryAfterS !== undefined ? { seconds: err.retryAfterS } : {};
}
