// Routes of the moderation feature. Owner: P0-W12 → P1-W06.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const BlamePage = lazy(() => import('./BlamePage'));
const ModQueuePage = lazy(() => import('./ModQueuePage'));
const ModLogPage = lazy(() => import('./ModLogPage'));
const ReportPage = lazy(() => import('./ReportPage'));
const RolesPage = lazy(() => import('./RolesPage'));

export const routes: RouteObject[] = [
  { path: '/blame', element: <BlamePage /> },
  { path: '/mod', element: <ModQueuePage /> },
  { path: '/mod/log', element: <ModLogPage /> },
  { path: '/mod/roles', element: <RolesPage /> },
  { path: '/report', element: <ReportPage /> },
];
