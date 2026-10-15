// Routes of the push feature. Owner: P1-W05.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table).
// Phase 1 feature (SCAFFOLD-1).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const PushSettingsPage = lazy(() => import('./PushSettingsPage'));

export const routes: RouteObject[] = [
  { path: '/settings/notifications', element: <PushSettingsPage /> },
];
