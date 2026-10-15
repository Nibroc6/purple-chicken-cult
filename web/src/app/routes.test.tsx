// Every planned route renders its (placeholder) page through the shell, and
// unknown routes render the client not-found page.
import { QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import { createMemoryRouter, RouterProvider, type RouteObject } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { i18n } from '../i18n/setup';
import { makeQueryClient } from './App';
import { featureRoutes } from './featureRoutes';
import { routes } from './routes';

function stubSession(authenticated: boolean) {
  vi.stubGlobal(
    'fetch',
    vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify(authenticated ? { authenticated: true, kind: 'user', csrf: 'x' } : { authenticated: false, kind: 'none' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      ),
    ),
  );
}

function renderAt(path: string) {
  const router = createMemoryRouter(routes, { initialEntries: [path] });
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

function paths(rs: RouteObject[]): string[] {
  return rs.flatMap((r) => (r.path ? [r.path.replace(':ref', 'abc')] : []));
}

describe('routes', () => {
  beforeEach(() => {
    stubSession(false);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('shows the landing page at / when signed out', async () => {
    renderAt('/');
    expect(await screen.findByRole('heading', { name: i18n.t('landing:page.title') })).toBeTruthy();
  });

  it('shows the feed at / when signed in', async () => {
    stubSession(true);
    renderAt('/');
    expect(await screen.findByRole('heading', { name: i18n.t('feed:page.title') })).toBeTruthy();
  });

  it.each(paths(featureRoutes))('renders %s', async (p) => {
    renderAt(p);
    expect(await screen.findByRole('heading', { level: 1 })).toBeTruthy();
    expect(screen.queryByText(i18n.t('common:notFound.title'))).toBeNull();
  });

  it('renders the not-found page for unknown routes', async () => {
    renderAt('/no/such/page');
    expect(await screen.findByRole('heading', { name: i18n.t('common:notFound.title') })).toBeTruthy();
  });
});
