// Join API calls (plan §5.3 invitesapi, invitee side). The flow runs in a guest
// session that joinStart creates; registerFinish replaces it with a user
// session whose CSRF token the client adopts. Owner: P0-W04 → P1-W02.
import { api, setCSRF, unwrap } from '../../api/client';
import { ApiError } from '../../api/errors';
import type { components } from '../../api/schema.gen';

export type SessionView = components['schemas']['SessionView'];
export type HandshakeState = components['schemas']['HandshakeStatus']['state'];

function ok(res: { response: Response; error?: unknown }): void {
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

export interface JoinStart {
  tosVersion: number;
  minAge: number;
}

export async function joinStart(): Promise<JoinStart> {
  const r = unwrap(await api.POST('/join/start', { body: {} }));
  setCSRF(r.csrf);
  return { tosVersion: r.tos_version, minAge: r.min_age };
}

export async function claim(payload: string): Promise<void> {
  ok(await api.POST('/join/claim', { body: { payload } }));
}

export interface JoinStatus {
  state: HandshakeState;
  sas?: string;
}

export async function joinStatus(): Promise<JoinStatus> {
  const st = unwrap(await api.GET('/join/status'));
  return { state: st.state, ...(st.sas ? { sas: st.sas } : {}) };
}

export interface JoinForm {
  handle: string;
  displayName: string;
  tosVersion: number;
  ageAttested: boolean;
  locale: string;
  languages: string[];
}

export async function registerBegin(f: JoinForm): Promise<unknown> {
  return unwrap(
    await api.POST('/join/register/begin', {
      body: {
        handle: f.handle,
        display_name: f.displayName,
        locale: f.locale,
        languages: f.languages,
        tos_version: f.tosVersion,
        age_attested: f.ageAttested,
      },
    }),
  ).options;
}

export async function registerFinish(credential: Record<string, unknown>): Promise<SessionView> {
  const sv = unwrap(await api.POST('/join/register/finish', { body: { credential } }));
  if (sv.csrf) setCSRF(sv.csrf);
  return sv;
}
