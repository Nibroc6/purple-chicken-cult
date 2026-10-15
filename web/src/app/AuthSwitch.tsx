// The "/" index route (SCAFFOLD; plan §6.4): the feed when signed in, the landing
// page otherwise, decided from /api/session. W07 owns FeedPage, W12 LandingPage.
import { lazy } from 'react';
import { Loading } from './Loading';
import { useSession } from './session';

const FeedPage = lazy(() => import('../features/feed/FeedPage'));
const LandingPage = lazy(() => import('../features/landing/LandingPage'));

export function AuthSwitch() {
  const { data, isPending } = useSession();
  if (isPending) return <Loading />;
  return data?.authenticated ? <FeedPage /> : <LandingPage />;
}
