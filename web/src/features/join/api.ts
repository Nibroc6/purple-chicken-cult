// Join API calls (plan §5.3 invitesapi, the claiming device's side). The flow
// of an invite runs in a guest session that joinStart creates;
// registerCosign replaces it with a user session whose CSRF token the client
// adopts. A friend+ upgrade runs in the friend's own signed-in session. Every
// call after the claim is signed by the device's key (X-PCC-Join-Sig).
// Owner: P0-W04 → P1-W02.
import { api, setCSRF, unwrap } from '../../api/client';
import { ApiError } from '../../api/errors';
import type { components } from '../../api/schema.gen';
import type { ClaimDevice, JoinCall } from '../invites/handshake';
import { b64url } from '../invites/handshake';
import { toStatusVM, type StatusVM } from '../invites/vm';

export type SessionView = components['schemas']['SessionView'];

function ok(res: { response: Response; error?: unknown }): void {
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

async function signed(dev: ClaimDevice, call: JoinCall) {
  return { header: { 'X-PCC-Join-Sig': await dev.call(call) } };
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

export async function claim(dev: ClaimDevice, payload: string): Promise<void> {
  ok(await api.POST('/join/claim', { body: await dev.claim(payload) }));
}

export async function joinStatus(dev: ClaimDevice): Promise<StatusVM> {
  return toStatusVM(unwrap(await api.GET('/join/status', { params: await signed(dev, 'status') })));
}

export async function audioStart(dev: ClaimDevice, kmac: Uint8Array): Promise<number> {
  return unwrap(
    await api.POST('/join/audio/start', { params: await signed(dev, 'audio_start'), body: { kmac: b64url(kmac) } }),
  ).attempt;
}

export async function dualFrame(dev: ClaimDevice, payload: string, kmac: Uint8Array): Promise<string> {
  return unwrap(
    await api.POST('/join/dual/frame', { params: await signed(dev, 'dual_frame'), body: { payload, kmac: b64url(kmac) } }),
  ).outcome;
}

export interface JoinForm {
  handle: string;
  displayName: string;
  tosVersion: number;
  ageAttested: boolean;
  locale: string;
  languages: string[];
}

export async function registerBegin(dev: ClaimDevice, f: JoinForm, signupToken: string): Promise<unknown> {
  return unwrap(
    await api.POST('/join/register/begin', {
      params: await signed(dev, 'register_begin'),
      body: {
        handle: f.handle,
        display_name: f.displayName,
        locale: f.locale,
        languages: f.languages,
        tos_version: f.tosVersion,
        age_attested: f.ageAttested,
        signup_token: signupToken,
      },
    }),
  ).options;
}

// Signup is the signup ceremony: get options for the new passkey over the drafts.
export interface Signup {
  options: unknown;
  records: unknown[];
}

export async function registerFinish(dev: ClaimDevice, credential: Record<string, unknown>): Promise<Signup> {
  const r = unwrap(
    await api.POST('/join/register/finish', { params: await signed(dev, 'register_finish'), body: { credential } }),
  );
  return { options: r.options, records: r.records };
}

export async function registerCosign(dev: ClaimDevice, assertion: Record<string, unknown>): Promise<SessionView> {
  const sv = unwrap(
    await api.POST('/join/register/cosign', { params: await signed(dev, 'register_cosign'), body: { assertion } }),
  );
  if (sv.csrf) setCSRF(sv.csrf);
  return sv;
}
