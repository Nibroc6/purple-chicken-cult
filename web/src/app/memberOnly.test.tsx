// Signed-out visitors on a members' page go to sign in, then come back.
import { QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { safeNext } from '../features/auth/LoginPage';
import { makeQueryClient } from './App';
import { routes } from './routes';
import { isPublicPath } from './Shell';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('members-only pages', () => {
  it('knows the public pages', () => {
    for (const p of ['/', '/login', '/join', '/about', '/terms', '/recover', '/enroll', '/about/']) expect(isPublicPath(p)).toBe(true);
    for (const p of ['/invite', '/invites', '/friends', '/settings', '/p/abc']) expect(isPublicPath(p)).toBe(false);
  });

  it('only follows same-site paths after signing in', () => {
    expect(safeNext('/invite')).toBe('/invite');
    expect(safeNext('/p/abc?x=1')).toBe('/p/abc?x=1');
    expect(safeNext(null)).toBe('/');
    expect(safeNext('https://evil.example')).toBe('/');
    expect(safeNext('//evil.example')).toBe('/');
    expect(safeNext('/\\evil.example')).toBe('/');
  });

  it('sends a signed-out visitor from a members page to sign in', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(new Response(JSON.stringify({ authenticated: false, kind: 'none' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))),
    );
    const router = createMemoryRouter(routes, { initialEntries: ['/invite'] });
    render(
      <QueryClientProvider client={makeQueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/login');
    });
    expect(router.state.location.search).toBe(`?next=${encodeURIComponent('/invite')}`);
    expect(await screen.findByTestId('login-join')).toBeTruthy();
  });
});
