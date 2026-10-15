// The route table (SCAFFOLD; plan §6.4). Features export their own `routes`
// arrays (features/<f>/routes.tsx, pre-listed in featureRoutes.ts); only "/" is
// owned here (AuthSwitch).
import type { RouteObject } from 'react-router';
import { AuthSwitch } from './AuthSwitch';
import { ErrorBoundary } from './ErrorBoundary';
import { featureRoutes } from './featureRoutes';
import { NotFound } from './NotFound';
import { Shell } from './Shell';

export const routes: RouteObject[] = [
  {
    element: <Shell />,
    errorElement: <ErrorBoundary />,
    children: [{ index: true, element: <AuthSwitch /> }, ...featureRoutes, { path: '*', element: <NotFound /> }],
  },
];
