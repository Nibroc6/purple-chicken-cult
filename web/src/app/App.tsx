// Root component (SCAFFOLD): react-query + the data router.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useState } from 'react';
import { createBrowserRouter, RouterProvider } from 'react-router';
import { ApiError } from '../api/errors';
import { routes } from './routes';

export function makeQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // Never retry a uniform 404 or a rate limit; retry network errors once.
        retry: (count, err) => !(err instanceof ApiError) && count < 1,
        refetchOnWindowFocus: false,
      },
    },
  });
}

export function App() {
  const [client] = useState(makeQueryClient);
  const [router] = useState(() => createBrowserRouter(routes));
  return (
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  );
}
