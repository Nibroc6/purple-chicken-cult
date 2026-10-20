// Runs the client-signal reporter while someone is signed in (signals.ts).
// Renders nothing. Owner: P1-W07.
import { useEffect } from 'react';
import { useSession } from '../../app/session';
import { startSignals } from './signals';

export function SignalsReporter() {
  const signedIn = useSession().data?.authenticated === true;
  useEffect(() => (signedIn ? startSignals() : undefined), [signedIn]);
  return null;
}
