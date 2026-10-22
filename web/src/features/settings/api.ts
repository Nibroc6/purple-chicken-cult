// Account API calls of the settings feature (plan §5.3 accountapi). Owner: P0-W11 → P1-W10.
import { api, unwrap } from '../../api/client';
import { ApiError } from '../../api/errors';
import type { components } from '../../api/schema.gen';
import type { CosignResult } from '../cosign';

export type SettingsView = components['schemas']['SettingsView'];
export type SettingsPatch = components['schemas']['SettingsPatch'];

export async function patchSettings(patch: SettingsPatch): Promise<SettingsView> {
  return unwrap(await api.PATCH('/me/settings', { body: patch }));
}

// exportAccount downloads the export zip (needs a fresh step-up).
export async function exportAccount(): Promise<Blob> {
  const res = await api.POST('/me/export', { body: {}, parseAs: 'blob' });
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
  return res.data as unknown as Blob;
}

// deleteAccount deletes the account; a passkey session sends the co-signature
// of the AccountDelete record (P1).
export async function deleteAccount(confirmHandle: string, cosig?: CosignResult): Promise<void> {
  const res = await api.POST('/me/delete', {
    body: { confirm_handle: confirmHandle, ...(cosig ? { intent: cosig.intent, assertion: cosig.assertion } : {}) },
  });
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

// sessionCosigns reports whether this session signed in with a passkey, so
// its account changes must be co-signed (P1).
export async function sessionCosigns(): Promise<boolean> {
  try {
    return unwrap(await api.GET('/auth/passkeys')).items.some((p) => p.current);
  } catch {
    return false;
  }
}

// fetchRecoveryPending returns when a pending recovery (manual or by
// guardians) of the caller's own account releases, or null.
export async function fetchRecoveryPending(): Promise<Date | null> {
  const r = unwrap(await api.GET('/me/recovery'));
  return r.pending_until ? new Date(r.pending_until) : null;
}

export async function cancelRecovery(): Promise<void> {
  const res = await api.POST('/me/recovery/cancel');
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

export type MeView = components['schemas']['MeView'];

// fetchMe reads GET /api/me: the signed-in user with every setting (the session
// view carries only a summary).
export async function fetchMe(): Promise<MeView> {
  const me = unwrap(await api.GET('/me'));
  const raw: { settings?: unknown } = me;
  if (typeof raw.settings !== 'object' || raw.settings === null) throw new ApiError(500, { error: 'internal' });
  return me;
}
