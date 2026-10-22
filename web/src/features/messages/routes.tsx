// Routes of the direct-messages feature. Owner: direct messages (contract
// request dm-1). Pages load lazily; app/routes.tsx imports this array.
import { lazy } from 'react';
import type { RouteObject } from 'react-router';

const MessagesPage = lazy(() => import('./MessagesPage'));
const NewConversationPage = lazy(() => import('./NewConversationPage'));
const ConversationPage = lazy(() => import('./ConversationPage'));

export const routes: RouteObject[] = [
  { path: '/messages', element: <MessagesPage /> },
  { path: '/messages/new', element: <NewConversationPage /> },
  { path: '/messages/:ref', element: <ConversationPage /> },
];
