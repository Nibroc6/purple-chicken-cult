// The passkey add and remove flows shared by the passkeys page and the
// second-passkey prompt (§17.2, §17.4). From Phase 1 a session started with a
// passkey co-signs each change with one of the account's EXISTING passkeys
// (the PasskeyChange record, so an export's chain of keys verifies offline):
// the server parks a new credential until that signature arrives, and a
// removal sends it along. Other sessions keep the fresh step-up of Phase 0.
// Owner: P1-W07.
import type { QueryClient } from '@tanstack/react-query';
import { fetchSession, sessionKey } from '../../app/session';
import { requestCosign } from '../cosign';
import { withStepUp } from '../stepup/store';
import { addPasskeyBegin, addPasskeyCosigned, addPasskeyFinish, removePasskey } from './api';
import { createPasskey } from './webauthn';

// CancelledError: the person closed the co-sign dialog (nothing changed).
export class CancelledError extends Error {
  constructor() {
    super('cancelled');
    this.name = 'CancelledError';
  }
}

// addPasskey runs the whole add flow; summary is the translated line the
// co-sign dialog shows above the record. It resolves once the passkey is stored.
export async function addPasskey(name: string, summary: string): Promise<void> {
  // The registration is retried after a step-up as a whole, never finish
  // alone: a session holds one ceremony, which the step-up replaces.
  const state = await withStepUp(async () => {
    const options = await addPasskeyBegin();
    const credential = await createPasskey(options);
    return addPasskeyFinish(credential, name);
  });
  if (state === 'added') return;
  const sig = await requestCosign({ purpose: 'passkey_add', summary });
  if (!sig) throw new CancelledError();
  await addPasskeyCosigned(sig);
}

// removeKey removes passkey id: co-signed when mustCosign (a session started
// with a passkey), else after a fresh step-up.
export async function removeKey(id: string, mustCosign: boolean, summary: string): Promise<void> {
  if (!mustCosign) {
    await withStepUp(() => removePasskey(id));
    return;
  }
  const sig = await requestCosign({ purpose: 'passkey_remove', target: id, summary });
  if (!sig) throw new CancelledError();
  await removePasskey(id, sig);
}

// settledSession waits for the app's session request to settle before a
// ceremony creates a new session (enrollment). A fresh browser's first
// /api/session answer comes from a request sent without any cookie; if it
// arrived after the enrollment session was created it would replace that
// session's CSRF token with none, and the next POST would get the uniform 404
// (the race of a fresh context loading /enroll#token).
export async function settledSession(qc: QueryClient): Promise<void> {
  await qc.query({ queryKey: sessionKey, queryFn: fetchSession, staleTime: 60_000 }).catch(() => undefined);
}
