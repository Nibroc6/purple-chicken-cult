// Invites API calls (plan §5.3 invitesapi, initiator side: invites, and the
// friend+ upgrades and recovery handshakes other features open).
// Owner: P0-W04 → P1-W02.
import { api, unwrap } from '../../api/client';
import { ApiError } from '../../api/errors';
import type { components } from '../../api/schema.gen';
import {
  toBudgetVM,
  toFrameVM,
  toInviteeVM,
  toStatusVM,
  type BudgetVM,
  type FrameVM,
  type InviteeVM,
  type StatusVM,
} from './vm';

type HandshakeView = components['schemas']['HandshakeView'];

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

export type HandshakeMode = 'qr_audio' | 'dual_qr';

// OpenedVM is an opened handshake as the initiator's device needs it.
export interface OpenedVM {
  hs: string;
  mode: HandshakeMode;
  frameKey: string;
  frameTTLms: number;
}

// toOpenedVM maps a HandshakeView (POST /invites/handshakes, or another
// feature's opener such as POST /friends/{ref}/upgrade).
export function toOpenedVM(hv: HandshakeView): OpenedVM {
  return {
    hs: hv.hs,
    mode: hv.mode === 'dual_qr' ? 'dual_qr' : 'qr_audio',
    frameKey: hv.frame_key ?? '',
    frameTTLms: hv.frame_ttl_ms,
  };
}

export async function openHandshake(mode: HandshakeMode, quiet: boolean): Promise<OpenedVM> {
  return toOpenedVM(unwrap(await api.POST('/invites/handshakes', { body: { mode, quiet } })));
}

export async function postEpub(hs: string, ei: string): Promise<void> {
  ok(await api.POST('/invites/handshakes/{hs}/epub', { ...at(hs), body: { e_i: ei } }));
}

export async function fetchFrame(hs: string): Promise<FrameVM> {
  return toFrameVM(unwrap(await api.GET('/invites/handshakes/{hs}/frame', at(hs))));
}

export async function fetchStatus(hs: string): Promise<StatusVM> {
  return toStatusVM(unwrap(await api.GET('/invites/handshakes/{hs}', at(hs))));
}

export async function fetchPeer(hs: string): Promise<string> {
  return unwrap(await api.GET('/invites/handshakes/{hs}/peer', at(hs))).e_v;
}

export async function confirmHandshake(hs: string): Promise<void> {
  ok(await api.POST('/invites/handshakes/{hs}/confirm', at(hs)));
}

export async function cancelHandshake(hs: string): Promise<void> {
  ok(await api.DELETE('/invites/handshakes/{hs}', at(hs)));
}

export type ReportIn = components['schemas']['InvitesReportIn'];
export type Outcome = components['schemas']['InvitesOutcome'];

export async function report(hs: string, body: ReportIn): Promise<Outcome> {
  return unwrap(await api.POST('/invites/handshakes/{hs}/report', { ...at(hs), body }));
}

export async function cosignHandshake(hs: string, intent: string, assertion: Record<string, unknown>): Promise<void> {
  ok(await api.POST('/invites/handshakes/{hs}/cosign', { ...at(hs), body: { intent, assertion } }));
}
