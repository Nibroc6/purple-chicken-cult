// Routes of the media feature. Owner: P1-W04.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const CheckPage = lazy(() => import('./CheckPage'));

export const routes: RouteObject[] = [{ path: '/media/check', element: <CheckPage /> }];
