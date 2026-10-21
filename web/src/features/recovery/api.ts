// Guardian recovery API calls (plan §5.3 accountapi, P1): the guardians'
// side (start in person, approve as the second guardian) and the recovering
// device's side (claim, status by ticket). Owner: P1-W10.
import { api, unwrap } from '../../api/client';
import { ApiError, errorKey } from '../../api/errors';
import type { components } from '../../api/schema.gen';
import { toOpenedVM, type OpenedVM } from '../invites';
import type { ClaimDevice } from '../invites/handshake';
import type { CosignResult } from '../cosign';
import { toGuardianRequestVM, toStatusVM, type GuardianRequestVM, type RecoveryStatusVM } from './vm';

type PersonView = components['schemas']['PersonView'];

function ok(res: { response: Response; error?: unknown }): void {
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

// FriendVM is a friend+ friend the viewer can start a recovery for.
export interface FriendVM {
  ref: string;
  handle: string;
  displayName: string;
}

// FRIEND_PAGES bounds how many friend pages the picker reads.
const FRIEND_PAGES = 10;

// fetchFriendPlus lists the viewer's friend+ friends (the accounts they can
// vouch for in person).
export async function fetchFriendPlus(): Promise<FriendVM[]> {
  const out: FriendVM[] = [];
  let cursor: string | undefined;
  for (let i = 0; i < FRIEND_PAGES; i++) {
    const page = unwrap(await api.GET('/friends', { params: { query: cursor ? { cursor } : {} } }));
    page.items
      .filter((p: PersonView) => p.relation === 'friend_plus')
      .forEach((p: PersonView) => out.push({ ref: p.ref, handle: p.handle, displayName: p.display_name }));
    if (!page.next) break;
    cursor = page.next;
  }
  return out;
}

// startRecovery opens a recovery of friend (guardian A, in person).
export async function startRecovery(friend: string): Promise<OpenedVM> {
  return toOpenedVM(unwrap(await api.POST('/recovery/sessions', { body: { friend } })));
}

export async function fetchGuardianRequests(): Promise<GuardianRequestVM[]> {
  return unwrap(await api.GET('/recovery/requests')).items.map(toGuardianRequestVM);
}

// approveRecovery is the second guardian's approval, co-signed when the
// session has a passkey.
export async function approveRecovery(id: string, cosig?: CosignResult): Promise<void> {
  ok(
    await api.POST('/recovery/requests/{id}/approve', {
      params: { path: { id } },
      ...(cosig ? { body: { intent: cosig.intent, assertion: cosig.assertion } } : {}),
    }),
  );
}

// claimRecovery claims the guardian's code from this device's guest session
// and returns the recovery ticket.
export async function claimRecovery(dev: ClaimDevice, payload: string): Promise<string> {
  return unwrap(await api.POST('/recovery/claim', { body: await dev.claim(payload) })).ticket;
}

export async function fetchRecoveryStatus(ticket: string): Promise<RecoveryStatusVM> {
  return toStatusVM(unwrap(await api.GET('/recovery/status', { params: { header: { 'X-PCC-Recovery': ticket } } })));
}

// The recovery Problem codes localized in the recovery namespace.
// i18n-dynamic: recovery:errors.
const codes = new Set(['recovery_exists', 'second_guardian_required', 'recovery_expired']);

export function recoveryErrorKey(err: unknown): string {
  if (err instanceof ApiError && codes.has(err.code)) return `recovery:errors.${err.code}`;
  return errorKey(err);
}
