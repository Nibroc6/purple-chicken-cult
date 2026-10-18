// Friends API calls of the people feature (plan §5.3 friendsapi). Owner: P0-W03 → P1-W03.
import { api, unwrap } from '../../api/client';
import { ApiError } from '../../api/errors';
import type { components } from '../../api/schema.gen';
import type { Ref } from '../../vm/types';
import { toOpenedVM, type OpenedVM } from '../invites';
import { toPersonVM, type PeopleVM } from './vm';

type Suggestion = components['schemas']['FriendsSuggestion'];

export interface FriendsPage {
  items: PeopleVM[];
  next?: string;
}

export interface RequestsVM {
  incoming: PeopleVM[];
  outgoing: PeopleVM[];
}

export interface SuggestionVM {
  person: PeopleVM;
  mutual: number;
}

const at = (ref: Ref) => ({ params: { path: { ref: ref as string } } });

function ok(res: { response: Response; error?: unknown }): void {
  if (!res.response.ok) throw new ApiError(res.response.status, res.error);
}

export async function fetchFriends(cursor?: string): Promise<FriendsPage> {
  const page = unwrap(await api.GET('/friends', { params: { query: cursor ? { cursor } : {} } }));
  return { items: page.items.map(toPersonVM), ...(page.next ? { next: page.next } : {}) };
}

export async function fetchRequests(): Promise<RequestsVM> {
  const r = unwrap(await api.GET('/friends/requests'));
  return { incoming: r.incoming.map(toPersonVM), outgoing: r.outgoing.map(toPersonVM) };
}

export async function fetchPerson(ref: Ref): Promise<PeopleVM> {
  return toPersonVM(unwrap(await api.GET('/people/{ref}', at(ref))));
}

export async function fetchBlocks(): Promise<PeopleVM[]> {
  return unwrap(await api.GET('/blocks')).items.map(toPersonVM);
}

export async function fetchSuggestions(): Promise<SuggestionVM[]> {
  return unwrap(await api.GET('/suggestions')).items.map((s: Suggestion) => ({
    person: toPersonVM(s.person),
    mutual: s.mutual,
  }));
}

// The person actions. Each resolves when the server accepted it.
export type PersonAction =
  | 'request'
  | 'withdraw'
  | 'accept'
  | 'decline'
  | 'unfriend'
  | 'block'
  | 'unblock'
  | 'mute'
  | 'unmute'
  | 'hide_via'
  | 'show_via';

// Cosig is the co-signature of a graph-changing action (P1): the intent and
// the passkey assertion from the co-sign dialog.
export interface Cosig {
  intent: string;
  assertion: Record<string, unknown>;
}

export async function runAction(action: PersonAction, ref: Ref, cosig?: Cosig): Promise<void> {
  const p = at(ref);
  const signed = cosig ? { ...p, body: cosig } : p;
  switch (action) {
    case 'request':
      ok(await api.POST('/people/{ref}/request', signed));
      return;
    case 'withdraw':
      ok(await api.DELETE('/people/{ref}/request', p));
      return;
    case 'accept':
      ok(await api.POST('/friends/requests/{ref}/accept', signed));
      return;
    case 'decline':
      ok(await api.POST('/friends/requests/{ref}/decline', p));
      return;
    case 'unfriend':
      ok(await api.DELETE('/friends/{ref}', signed));
      return;
    case 'block':
      ok(await api.PUT('/people/{ref}/block', signed));
      return;
    case 'unblock':
      ok(await api.DELETE('/people/{ref}/block', p));
      return;
    case 'mute':
      ok(await api.PUT('/people/{ref}/mute', p));
      return;
    case 'unmute':
      ok(await api.DELETE('/people/{ref}/mute', p));
      return;
    case 'hide_via':
      ok(await api.PUT('/people/{ref}/hide-via', p));
      return;
    case 'show_via':
      ok(await api.DELETE('/people/{ref}/hide-via', p));
      return;
  }
}

// openUpgrade opens a friend+ upgrade handshake with a plain friend.
export async function openUpgrade(ref: Ref): Promise<OpenedVM> {
  return toOpenedVM(unwrap(await api.POST('/friends/{ref}/upgrade', at(ref))));
}

// sessionCosigns reports whether this session signed in with a passkey: its
// graph changes are then co-signed (a session without one — test hooks only —
// is not asked).
export async function sessionCosigns(): Promise<boolean> {
  try {
    return unwrap(await api.GET('/auth/passkeys')).items.some((p) => p.current);
  } catch {
    return false;
  }
}
