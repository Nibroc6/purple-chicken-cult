// The sign-in gate: signed out, member pages show a sign-in prompt; a session
// that ends mid-visit (found through a 404) says so and keeps the page; signing
// in again in place brings the page back; being offline is not signing out.
import { QueryClientProvider, useQueryClient } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Outlet, createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from '../../api/client';
import { makeQueryClient } from '../../app/App';
import { isMemberPath } from '../../app/memberPaths';
import { markSignedOut } from '../../app/session';
import { i18n } from '../../i18n/setup';
import LoginPage, { nextPath } from './LoginPage';
import { SignInGate } from './SignInGate';

const wa = vi.hoisted(() => ({ startAuthentication: vi.fn() }));
vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: () => true,
  browserSupportsWebAuthnAutofill: () => Promise.resolve(false),
  startAuthentication: wa.startAuthentication,
  startRegistration: vi.fn(),
  WebAuthnAbortService: { cancelCeremony: vi.fn(), createNewAbortSignal: () => new AbortController().signal },
}));

const signedIn = { authenticated: true, kind: 'user', csrf: 'csrf-user', me: { handle: 'alice' } };
const signedOut = { authenticated: false, kind: 'none' };

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

// world is what the stubbed server answers; paths lists every request.
const world = { session: signedIn as unknown, offline: false, paths: [] as string[] };

function stubAPI() {
  vi.stubGlobal(
    'fetch',
    vi.fn((req: Request) => {
      const path = new URL(req.url).pathname.replace(/^\/api/, '');
      world.paths.push(path);
      if (world.offline) return Promise.reject(new TypeError('Failed to fetch'));
      if (path === '/session') return Promise.resolve(json(200, world.session));
      if (path === '/auth/login/begin') return Promise.resolve(json(200, { options: { publicKey: { challenge: 'Y2g', rpId: 'localhost' } } }));
      if (path === '/auth/login/finish') {
        world.session = signedIn;
        return Promise.resolve(json(200, signedIn));
      }
      return Promise.resolve(json(404, { error: 'not_found' }));
    }),
  );
}

// A member page with local state and a request that may 404.
function Notes() {
  const qc = useQueryClient();
  return (
    <section>
      <h1>notes page</h1>
      <input aria-label="draft" />
      <button type="button" onClick={() => void api.GET('/notifications', { params: { query: {} } }).catch(() => undefined)}>
        load
      </button>
      <button
        type="button"
        onClick={() => {
          markSignedOut(qc);
        }}
      >
        sign out
      </button>
    </section>
  );
}

function renderAt(path: string) {
  const router = createMemoryRouter(
    [
      {
        element: (
          <SignInGate>
            <Outlet />
          </SignInGate>
        ),
        children: [
          { path: '/', element: <Notes /> },
          { path: '/notifications', element: <Notes /> },
          { path: '/about', element: <p>about page</p> },
          { path: '/login', element: <LoginPage /> },
        ],
      },
    ],
    { initialEntries: [path] },
  );
  render(
    <QueryClientProvider client={makeQueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

beforeEach(() => {
  world.session = signedIn;
  world.offline = false;
  world.paths = [];
  wa.startAuthentication.mockReset().mockResolvedValue({ id: 'cred', rawId: 'cred', type: 'public-key', response: {}, clientExtensionResults: {} });
  stubAPI();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('sign-in gate', () => {
  it('shows a sign-in prompt on a member page when signed out', async () => {
    world.session = signedOut;
    renderAt('/notifications');
    const prompt = await screen.findByTestId('signin-prompt');
    expect(prompt.dataset.ended).toBe('false');
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe(i18n.t('auth:gate.title'));
    expect(screen.queryByText('notes page')).toBeNull();
  });

  it('leaves public pages alone when signed out', async () => {
    world.session = signedOut;
    renderAt('/about');
    await screen.findByText('about page');
    expect(screen.queryByTestId('signin-prompt')).toBeNull();
  });

  it('says the session ended when a 404 turns out to be a sign-out, and signs in again in place', async () => {
    renderAt('/notifications');
    const draft = await screen.findByLabelText<HTMLInputElement>('draft');
    fireEvent.change(draft, { target: { value: 'half a post' } });
    world.session = signedOut;
    fireEvent.click(screen.getByText('load'));
    const prompt = await screen.findByTestId('signin-prompt');
    expect(prompt.dataset.ended).toBe('true');
    expect(prompt.textContent).toContain(i18n.t('auth:gate.endedTitle'));
    // The page is kept, hidden.
    expect(draft.closest('[hidden]')).not.toBeNull();

    fireEvent.click(screen.getByTestId('signin-prompt-button'));
    await waitFor(() => {
      expect(screen.queryByTestId('signin-prompt')).toBeNull();
    });
    expect(world.paths).toContain('/auth/login/finish');
    expect(screen.getByLabelText<HTMLInputElement>('draft').value).toBe('half a post');
    expect(draft.closest('[hidden]')).toBeNull();
  });

  it('covers the feed page too once the session has ended', async () => {
    renderAt('/');
    await screen.findByText('notes page');
    world.session = signedOut;
    fireEvent.click(screen.getByText('load'));
    expect((await screen.findByTestId('signin-prompt')).dataset.ended).toBe('true');
  });

  it('does not read being offline as signed out', async () => {
    renderAt('/notifications');
    await screen.findByText('notes page');
    world.offline = true;
    fireEvent.click(screen.getByText('load'));
    await waitFor(() => {
      expect(world.paths.filter((p) => p === '/session').length).toBeGreaterThan(0);
    });
    document.dispatchEvent(new Event('visibilitychange'));
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByTestId('signin-prompt')).toBeNull();
    expect(screen.getByText('notes page')).toBeTruthy();
  });

  it('shows no "session ended" prompt after signing out on purpose', async () => {
    renderAt('/');
    await screen.findByText('notes page');
    fireEvent.click(screen.getByText('sign out'));
    await new Promise((r) => setTimeout(r, 50));
    // "/" is left to the landing page (AuthSwitch; here the stand-in page).
    expect(screen.queryByTestId('signin-prompt')).toBeNull();
    expect(screen.getByText('notes page')).toBeTruthy();
  });

  it('rechecks the session at most once per interval', async () => {
    renderAt('/notifications');
    await screen.findByText('notes page');
    const before = world.paths.filter((p) => p === '/session').length;
    fireEvent.click(screen.getByText('load'));
    fireEvent.click(screen.getByText('load'));
    fireEvent.click(screen.getByText('load'));
    await waitFor(() => {
      expect(world.paths.filter((p) => p === '/notifications').length).toBe(3);
    });
    await new Promise((r) => setTimeout(r, 50));
    expect(world.paths.filter((p) => p === '/session').length).toBe(before + 1);
  });
});

describe('login page next', () => {
  it('accepts only paths on this site', () => {
    expect(nextPath('/notifications?x=1')).toBe('/notifications?x=1');
    expect(nextPath(null)).toBe('/');
    expect(nextPath('https://evil.example/')).toBe('/');
    expect(nextPath('//evil.example/')).toBe('/');
    expect(nextPath('/\\evil.example/')).toBe('/');
  });

  it('goes to next when already signed in', async () => {
    const router = renderAt('/login?next=/about');
    await screen.findByText('about page');
    expect(router.state.location.pathname).toBe('/about');
  });
});

describe('member paths', () => {
  it('matches whole path segments', () => {
    expect(isMemberPath('/settings')).toBe(true);
    expect(isMemberPath('/settings/passkeys')).toBe(true);
    expect(isMemberPath('/p/abc')).toBe(true);
    expect(isMemberPath('/people/x')).toBe(true);
    expect(isMemberPath('/privacy')).toBe(false);
    expect(isMemberPath('/login')).toBe(false);
    expect(isMemberPath('/join')).toBe(false);
    expect(isMemberPath('/')).toBe(false);
  });
});
