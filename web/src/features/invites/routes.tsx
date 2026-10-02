// Routes of the invites feature. Owner: P0-W04 → P1-W02.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const InvitePage = lazy(() => import('./InvitePage'));
const InviteesPage = lazy(() => import('./InviteesPage'));

export const routes: RouteObject[] = [
  { path: '/invite', element: <InvitePage /> },
  { path: '/invite/people', element: <InviteesPage /> },
];
