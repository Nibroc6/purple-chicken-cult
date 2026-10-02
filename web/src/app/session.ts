// Session state (SCAFFOLD): GET /api/session via react-query; keeps the CSRF
// token in the API client.
import { useQuery } from '@tanstack/react-query';
import { api, setCSRF, unwrap } from '../api/client';
import type { components } from '../api/schema.gen';

export type SessionView = components['schemas']['SessionView'];
export type MeView = components['schemas']['MeView'];

export const sessionKey = ['session'] as const;

export async function fetchSession(): Promise<SessionView> {
  const s = unwrap(await api.GET('/session'));
  setCSRF(s.csrf);
  return s;
}

export function useSession() {
  return useQuery({ queryKey: sessionKey, queryFn: fetchSession, staleTime: 60_000 });
}

export function useMe(): MeView | undefined {
  return useSession().data?.me;
}
