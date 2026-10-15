// Routes of the composer feature. Owner: P0-W05 → P1-W12.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const ComposePage = lazy(() => import('./ComposePage'));

export const routes: RouteObject[] = [
  { path: '/compose', element: <ComposePage /> },
];
