// Routes of the post feature. Owner: P0-W05 → P1-W12.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table). /p/:ref itself is the family tree (P0-W08).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const ManagePostPage = lazy(() => import('./ManagePostPage'));

export const routes: RouteObject[] = [{ path: '/p/:ref/edit', element: <ManagePostPage /> }];
