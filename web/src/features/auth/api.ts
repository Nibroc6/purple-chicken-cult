// Auth API calls (plan §5.3 authapi). Owner: P0-W01 → P1-W07. Every ceremony
// that starts a new session (login, enrollment) hands back that session's CSRF
// token, which the client must use from then on.
import { api, setCSRF, unwrap } from '../../api/client';
import { ApiError } from '../../api/errors';
import type { components } from '../../api/schema.gen';

export type SessionView = components['schemas']['SessionView'];
export type PasskeyJSON = components['schemas']['AuthPasskey'];
export type Credential = Record<string, unknown>;

function noContent(res: { response: Response; error?: unknown }): void {
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

// adopt stores the CSRF token of a session the server just created.
function adopt<T extends { csrf?: string }>(v: T): T {
  if (v.csrf) setCSRF(v.csrf);
  return v;
}

export async function loginBegin(): Promise<unknown> {
  return adopt(unwrap(await api.POST('/auth/login/begin', { body: {} }))).options;
}

export async function loginFinish(credential: Credential): Promise<SessionView> {
  return adopt(unwrap(await api.POST('/auth/login/finish', { body: { credential } })));
}

export async function logout(): Promise<void> {
  noContent(await api.POST('/auth/logout'));
  setCSRF(undefined);
}

export async function stepUpBegin(): Promise<unknown> {
  return unwrap(await api.POST('/auth/stepup/begin', { body: {} })).options;
}

export async function stepUpFinish(credential: Credential): Promise<void> {
  noContent(await api.POST('/auth/stepup/finish', { body: { credential } }));
}

export async function listPasskeys(): Promise<PasskeyJSON[]> {
  return unwrap(await api.GET('/auth/passkeys')).items;
}

export async function addPasskeyBegin(): Promise<unknown> {
  return unwrap(await api.POST('/auth/passkeys/begin', { body: {} })).options;
}

// Cosig is a co-signature from the cosign dialog: {intent, assertion}.
export interface Cosig {
  intent: string;
  assertion: Record<string, unknown>;
}

// addPasskeyFinish verifies the new credential. It resolves "cosign" when the
// server parked it until an existing passkey co-signs the change (P1).
export async function addPasskeyFinish(credential: Credential, name: string): Promise<'cosign' | 'added'> {
  const res = await api.POST('/auth/passkeys/finish', { body: { credential, name } });
  if (res.response.status === 202) return 'cosign';
  noContent(res);
  return 'added';
}

// addPasskeyCosigned stores the parked credential with its co-signature.
export async function addPasskeyCosigned(c: Cosig): Promise<void> {
  noContent(await api.POST('/auth/passkeys/finish', { body: { intent: c.intent, assertion: c.assertion } }));
}

export async function removePasskey(id: string, c?: Cosig): Promise<void> {
  noContent(
    await api.DELETE('/auth/passkeys/{id}', {
      params: { path: { id } },
      ...(c ? { body: { intent: c.intent, assertion: c.assertion } } : {}),
    }),
  );
}

export interface EnrollStart {
  options: unknown;
  purpose: 'genesis' | 'recovery';
  tosVersion: number;
  minAge: number;
  existing: PasskeyJSON[];
}

export async function enrollBegin(token: string): Promise<EnrollStart> {
  const r = adopt(unwrap(await api.POST('/enroll/begin', { body: { token } })));
  return {
    options: r.options,
    purpose: r.purpose,
    tosVersion: r.tos_version ?? 0,
    minAge: r.min_age ?? 0,
    existing: r.existing_passkeys ?? [],
  };
}

export interface EnrollFinish {
  credential: Credential;
  tosVersion?: number;
  ageAttested?: boolean;
}

export async function enrollFinish(f: EnrollFinish): Promise<SessionView> {
  return adopt(
    unwrap(
      await api.POST('/enroll/finish', {
        body: {
          credential: f.credential,
          tos_version: f.tosVersion ?? 0,
          age_attested: f.ageAttested ?? false,
        },
      }),
    ),
  );
}
