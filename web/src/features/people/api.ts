// Friends API calls of the people feature (plan §5.3 friendsapi). Owner: P0-W03 → P1-W03.
import { api, unwrap } from '../../api/client';
import { ApiError } from '../../api/errors';
import type { components } from '../../api/schema.gen';
import type { PersonVM, Ref } from '../../vm/types';
import { toPersonVM } from './vm';

type Suggestion = components['schemas']['FriendsSuggestion'];

export interface FriendsPage {
  items: PersonVM[];
  next?: string;
}

export interface RequestsVM {
  incoming: PersonVM[];
  outgoing: PersonVM[];
}

export interface SuggestionVM {
  person: PersonVM;
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

export async function fetchPerson(ref: Ref): Promise<PersonVM> {
  return toPersonVM(unwrap(await api.GET('/people/{ref}', at(ref))));
}

export async function fetchBlocks(): Promise<PersonVM[]> {
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
  | 'unmute';

export async function runAction(action: PersonAction, ref: Ref): Promise<void> {
  const p = at(ref);
  switch (action) {
    case 'request':
      ok(await api.POST('/people/{ref}/request', p));
      return;
    case 'withdraw':
      ok(await api.DELETE('/people/{ref}/request', p));
      return;
    case 'accept':
      ok(await api.POST('/friends/requests/{ref}/accept', p));
      return;
    case 'decline':
      ok(await api.POST('/friends/requests/{ref}/decline', p));
      return;
    case 'unfriend':
      ok(await api.DELETE('/friends/{ref}', p));
      return;
    case 'block':
      ok(await api.PUT('/people/{ref}/block', p));
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
  }
}
