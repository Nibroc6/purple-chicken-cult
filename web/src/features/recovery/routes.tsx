// Routes of the recovery feature. Owner: P1-W10.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table).
// Phase 1 feature (SCAFFOLD-1).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const RecoverPage = lazy(() => import('./RecoverPage'));
const RecoverySettingsPage = lazy(() => import('./RecoverySettingsPage'));

export const routes: RouteObject[] = [
  { path: '/recover', element: <RecoverPage /> },
  { path: '/settings/recovery', element: <RecoverySettingsPage /> },
];
