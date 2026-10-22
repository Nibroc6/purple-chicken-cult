// Routes of the handshake-audio feature. Owner: P1-W13.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const SoundCheckPage = lazy(() => import('./SoundCheckPage'));

export const routes: RouteObject[] = [{ path: '/sound/check', element: <SoundCheckPage /> }];
