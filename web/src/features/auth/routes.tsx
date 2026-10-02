// Routes of the auth feature. Owner: P0-W01 → P1-W07.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const LoginPage = lazy(() => import('./LoginPage'));
const EnrollPage = lazy(() => import('./EnrollPage'));
const PasskeysPage = lazy(() => import('./PasskeysPage'));

export const routes: RouteObject[] = [
  { path: '/login', element: <LoginPage /> },
  { path: '/enroll', element: <EnrollPage /> },
  { path: '/settings/passkeys', element: <PasskeysPage /> },
];
