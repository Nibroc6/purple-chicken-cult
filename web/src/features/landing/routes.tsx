// Routes of the landing feature. Owner: P0-W12.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const TermsPage = lazy(() => import('./TermsPage'));

export const routes: RouteObject[] = [
  { path: '/terms', element: <TermsPage /> },
];
