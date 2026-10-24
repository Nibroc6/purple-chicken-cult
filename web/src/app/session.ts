// Session state (SCAFFOLD): GET /api/session via react-query; keeps the CSRF
// token in the API client. The session watch notices a session that ended
// while the tab was open (expired, or signed out elsewhere): the API answers
// that with uniform 404s, so any 404, a dropped event stream or the tab
// coming back into view asks /api/session again. Only its answer decides:
// when the network is down that request fails and the cached session stays,
// so being offline never reads as signed out.
import { hashKey, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';
import { api, onNotFound, setCSRF, unwrap } from '../api/client';
import { onStreamError } from '../api/sse';
import type { components } from '../api/schema.gen';

export type SessionView = components['schemas']['SessionView'];
export type MeView = components['schemas']['MeView'];

export const sessionKey = ['session'] as const;

// At most one recheck per this interval, however many requests 404.
export const RECHECK_MS = 5_000;

export async function fetchSession(): Promise<SessionView> {
  const s = unwrap(await api.GET('/session'));
  setCSRF(s.csrf);
  return s;
}

// Whether this tab was signed in before the current signed-out state (so the
// session ended rather than never having started). The session watch sets it
// from the query cache; a deliberate sign-out clears it (markSignedOut).
let signedInThisVisit = false;

export function useSession() {
  return useQuery({ queryKey: sessionKey, queryFn: fetchSession, staleTime: 60_000 });
}

export function useMe(): MeView | undefined {
  return useSession().data?.me;
}

// sessionEnded: signed out now, after being signed in earlier in this tab.
export function sessionEnded(s: SessionView | undefined): boolean {
  return s?.authenticated === false && signedInThisVisit;
}

// markSignedOut records a sign-out the person asked for (no "session ended"
// prompt follows it).
export function markSignedOut(qc: QueryClient): void {
  signedInThisVisit = false;
  qc.setQueryData(sessionKey, { authenticated: false, kind: 'none' });
}

// adoptSession takes the session a sign-in returned and refetches everything
// else, which was loaded (or refused) under the session before it.
export function adoptSession(qc: QueryClient, sv: SessionView): void {
  qc.setQueryData(sessionKey, sv);
  void qc.invalidateQueries({ predicate: (q) => q.queryKey[0] !== sessionKey[0] });
}

// useSessionWatch rechecks the session on the cues above while signed in.
export function useSessionWatch(): void {
  const qc = useQueryClient();
  useEffect(() => {
    const note = (data: unknown) => {
      if ((data as SessionView | undefined)?.authenticated) signedInThisVisit = true;
    };
    note(qc.getQueryData(sessionKey));
    // Runs on every cache update, before the components that read it render.
    const offCache = qc.getQueryCache().subscribe((e) => {
      if (e.type === 'updated' && e.query.queryHash === hashKey(sessionKey)) note(e.query.state.data);
    });
    let last = 0;
    const recheck = () => {
      const s = qc.getQueryData<SessionView>(sessionKey);
      const now = Date.now();
      if (!s?.authenticated || now - last < RECHECK_MS) return;
      last = now;
      void qc.invalidateQueries({ queryKey: sessionKey });
    };
    const onVisible = () => {
      if (document.visibilityState === 'visible') recheck();
    };
    const offNotFound = onNotFound(recheck);
    const offStream = onStreamError(recheck);
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      offCache();
      offNotFound();
      offStream();
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [qc]);
}
