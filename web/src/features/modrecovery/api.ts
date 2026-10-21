// Manual recovery API (moderators; plan §5.3 accountapi). Owner: P0-W11 → P1-W10.
import { api, unwrap } from '../../api/client';
import { ApiError, errorKey } from '../../api/errors';
import type { CosignResult } from '../cosign';

// openRecovery opens a recovery for subject (a moderator-scoped account ref)
// and returns the request ID every moderator can use.
export async function openRecovery(subject: string): Promise<string> {
  return unwrap(await api.POST('/mod/recovery', { body: { subject } })).id;
}

// approveRecovery is the second moderator's approval, co-signed (the
// RecoveryApproval record) when the session has a passkey (P1).
export async function approveRecovery(id: string, cosig?: CosignResult): Promise<void> {
  const res = await api.POST('/mod/recovery/{id}/approve', {
    params: { path: { id } },
    ...(cosig ? { body: { intent: cosig.intent, assertion: cosig.assertion } } : {}),
  });
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

export async function recoveryLink(id: string): Promise<string> {
  return unwrap(await api.GET('/mod/recovery/{id}/link', { params: { path: { id } } })).enroll_url;
}

// isRef reports whether s looks like an opaque API ref (anything else would
// only ever answer the uniform not-found).
export function isRef(s: string): boolean {
  return /^[A-Za-z0-9_-]+$/.test(s);
}

// The recovery Problem codes localized in the modrecovery namespace.
// i18n-dynamic: modrecovery:errors.
const codes = new Set([
  'recovery_self',
  'recovery_exists',
  'second_moderator_required',
  'recovery_not_open',
  'recovery_expired',
  'recovery_held',
  'recovery_link_issued',
]);

export function recoveryErrorKey(err: unknown): string {
  if (err instanceof ApiError && codes.has(err.code)) return `modrecovery:errors.${err.code}`;
  return errorKey(err);
}
