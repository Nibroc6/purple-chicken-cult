// Routes of the join feature. Owner: P0-W04 → P1-W02.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const JoinPage = lazy(() => import('./JoinPage'));

export const routes: RouteObject[] = [
  { path: '/join', element: <JoinPage /> },
];
