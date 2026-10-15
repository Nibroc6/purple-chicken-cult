// Routes of the settings feature. Owner: P0-W11 → P1-W10.
// Pages load lazily; app/routes.tsx imports this array (features never edit the
// shared route table).
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const SettingsPage = lazy(() => import('./SettingsPage'));
const DisplayPage = lazy(() => import('./DisplayPage'));
const LanguagesPage = lazy(() => import('./LanguagesPage'));
const PrivacyPage = lazy(() => import('./PrivacyPage'));
const ExportPage = lazy(() => import('./ExportPage'));
const DeletePage = lazy(() => import('./DeletePage'));

export const routes: RouteObject[] = [
  { path: '/settings', element: <SettingsPage /> },
  { path: '/settings/display', element: <DisplayPage /> },
  { path: '/settings/languages', element: <LanguagesPage /> },
  { path: '/settings/privacy', element: <PrivacyPage /> },
  { path: '/settings/export', element: <ExportPage /> },
  { path: '/settings/delete', element: <DeletePage /> },
];
