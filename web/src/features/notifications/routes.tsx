// Routes of the notifications feature. Owner: P0-W10 → P1-W05.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const NotificationsPage = lazy(() => import('./NotificationsPage'));

export const routes: RouteObject[] = [
  { path: '/notifications', element: <NotificationsPage /> },
];
