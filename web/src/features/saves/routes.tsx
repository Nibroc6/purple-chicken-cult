// Routes of the saves feature. Owner: P1-W08.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table).
// Phase 1 feature (SCAFFOLD-1).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const SavesPage = lazy(() => import('./SavesPage'));

export const routes: RouteObject[] = [
  { path: '/saves', element: <SavesPage /> },
];
