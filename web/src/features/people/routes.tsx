// Routes of the people feature. Owner: P0-W03 → P1-W03.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const PersonPage = lazy(() => import('./PersonPage'));
const FriendsPage = lazy(() => import('./FriendsPage'));
const RequestsPage = lazy(() => import('./RequestsPage'));
const SuggestionsPage = lazy(() => import('./SuggestionsPage'));
const BlocksPage = lazy(() => import('./BlocksPage'));

export const routes: RouteObject[] = [
  { path: '/people/:ref', element: <PersonPage /> },
  { path: '/friends', element: <FriendsPage /> },
  { path: '/friends/requests', element: <RequestsPage /> },
  { path: '/suggestions', element: <SuggestionsPage /> },
  { path: '/settings/blocks', element: <BlocksPage /> },
];
