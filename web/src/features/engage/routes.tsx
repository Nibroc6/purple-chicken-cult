// Routes of the engage feature. Owner: P0-W06 → P1-W08.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table). /p/:ref itself is the family tree (P0-W08).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const LikesPage = lazy(() => import('./LikesPage'));
const HiddenPage = lazy(() => import('./HiddenPage'));

export const routes: RouteObject[] = [
  { path: '/p/:ref/likes', element: <LikesPage /> },
  { path: '/settings/hidden', element: <HiddenPage /> },
];
