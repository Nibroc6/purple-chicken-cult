// Routes of the themes feature. Owner: P1-W09.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table).
// Phase 1 feature (SCAFFOLD-1).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const ThemePage = lazy(() => import('./ThemePage'));

export const routes: RouteObject[] = [
  { path: '/settings/theme', element: <ThemePage /> },
];
