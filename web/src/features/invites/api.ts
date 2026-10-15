// Invites API calls (plan §5.3 invitesapi, inviter side). Owner: P0-W04 → P1-W02.
import { api, unwrap } from '../../api/client';
import { ApiError } from '../../api/errors';
import { toBudgetVM, toFrameVM, toInviteeVM, type BudgetVM, type FrameVM, type HandshakeState, type InviteeVM } from './vm';

function ok(res: { response: Response; error?: unknown }): void {
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

const at = (hs: string) => ({ params: { path: { hs } } });

export async function fetchBudget(): Promise<BudgetVM> {
  return toBudgetVM(unwrap(await api.GET('/invites/budget')));
}

export async function fetchInvitees(): Promise<InviteeVM[]> {
  return unwrap(await api.GET('/invites/invitees')).items.map(toInviteeVM);
}

export interface OpenedVM {
  hs: string;
  frameTTLms: number;
}

export async function openHandshake(): Promise<OpenedVM> {
  const hv = unwrap(await api.POST('/invites/handshakes', { body: { mode: 'qr' } }));
  return { hs: hv.hs, frameTTLms: hv.frame_ttl_ms };
}

export async function fetchFrame(hs: string): Promise<FrameVM> {
  return toFrameVM(unwrap(await api.GET('/invites/handshakes/{hs}/frame', at(hs))));
}

export interface StatusVM {
  state: HandshakeState;
  sas?: string;
}

export async function fetchStatus(hs: string): Promise<StatusVM> {
  const st = unwrap(await api.GET('/invites/handshakes/{hs}', at(hs)));
  return { state: st.state, ...(st.sas ? { sas: st.sas } : {}) };
}

export async function confirmHandshake(hs: string): Promise<void> {
  ok(await api.POST('/invites/handshakes/{hs}/confirm', at(hs)));
}

export async function cancelHandshake(hs: string): Promise<void> {
  ok(await api.DELETE('/invites/handshakes/{hs}', at(hs)));
}
