// Routes of the seen feature. Owner: P0-W06 → P1-W08.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const SeenPage = lazy(() => import('./SeenPage'));

export const routes: RouteObject[] = [
  { path: '/seen', element: <SeenPage /> },
];
