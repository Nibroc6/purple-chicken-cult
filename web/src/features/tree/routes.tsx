// Routes of the tree feature. Owner: P0-W08 → P1-W09.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const TreePage = lazy(() => import('./TreePage'));

export const routes: RouteObject[] = [
  { path: '/p/:ref', element: <TreePage /> },
];
