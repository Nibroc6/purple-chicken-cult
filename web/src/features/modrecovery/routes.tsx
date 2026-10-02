// Routes of the modrecovery feature. Owner: P0-W11 → P1-W10.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const ModRecoveryPage = lazy(() => import('./ModRecoveryPage'));

export const routes: RouteObject[] = [
  { path: '/mod/recovery', element: <ModRecoveryPage /> },
];
