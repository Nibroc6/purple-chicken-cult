// Auth feature: view-models, the WebAuthn wrapper and the pages against a
// stubbed API and a stubbed @simplewebauthn/browser.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/errors';
import { makeQueryClient } from '../../app/App';
import { useSession } from '../../app/session';
import { i18n } from '../../i18n/setup';
import { StepUpModal } from '../stepup/StepUpModal';
import EnrollPage from './EnrollPage';
import LoginPage from './LoginPage';
import PasskeysPage from './PasskeysPage';
import { authErrorKey, readFragmentToken, toPasskeyVM } from './vm';
import { PasskeyError, createPasskey, getAssertion } from './webauthn';

const wa = vi.hoisted(() => ({
  supported: true,
  autofill: false,
  startAuthentication: vi.fn(),
  startRegistration: vi.fn(),
  cancelCeremony: vi.fn(),
}));

const cosign = vi.hoisted(() => ({ requestCosign: vi.fn() }));
vi.mock('../cosign', () => ({ requestCosign: cosign.requestCosign }));

vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: () => wa.supported,
  browserSupportsWebAuthnAutofill: () => Promise.resolve(wa.autofill),
  startAuthentication: wa.startAuthentication,
  startRegistration: wa.startRegistration,
  WebAuthnAbortService: { cancelCeremony: wa.cancelCeremony, createNewAbortSignal: () => new AbortController().signal },
}));

interface Call {
  method: string;
  path: string;
  body: unknown;
  csrf: string | null;
}

type Handler = (c: Call) => Response | undefined;

function json(status: number, body: unknown): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const signedIn = { authenticated: true, kind: 'user', csrf: 'csrf-user', me: { handle: 'alice' } };
const signedOut = { authenticated: false, kind: 'none' };

function stubAPI(session: unknown, handler: Handler): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const url = new URL(req.url);
      const path = url.pathname.replace(/^\/api/, '');
      const text = await req.text();
      const c: Call = {
        method: req.method,
        path,
        body: text ? (JSON.parse(text) as unknown) : undefined,
        csrf: req.headers.get('X-PCC-CSRF'),
      };
      calls.push(c);
      if (path === '/session') return json(200, session);
      return handler(c) ?? json(404, { error: 'not_found' });
    }),
  );
  return calls;
}

function renderAt(path: string, el: ReactNode) {
  const router = createMemoryRouter(
    [
      { path, element: el },
      { path: '/', element: <p>home</p> },
      { path: '/login', element: <p>login page</p> },
    ],
    { initialEntries: [path] },
  );
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <RouterProvider router={router} />
      <StepUpModal />
    </QueryClientProvider>,
  );
}

const loginOptions = { publicKey: { challenge: 'Y2hhbGxlbmdl', rpId: 'localhost', userVerification: 'required' }, mediation: 'conditional' };
const createOptions = {
  publicKey: { challenge: 'Y2hh', rp: { id: 'localhost', name: 'pcc' }, user: { id: 'dQ', name: 'alice', displayName: 'alice' }, pubKeyCredParams: [] },
};
const assertion = { id: 'cred', rawId: 'cred', type: 'public-key', response: {}, clientExtensionResults: {} };
const attestation = { id: 'new', rawId: 'new', type: 'public-key', response: {}, clientExtensionResults: {} };

beforeEach(() => {
  wa.supported = true;
  wa.autofill = false;
  wa.startAuthentication.mockReset().mockResolvedValue(assertion);
  wa.startRegistration.mockReset().mockResolvedValue(attestation);
  wa.cancelCeremony.mockReset();
  cosign.requestCosign.mockReset().mockResolvedValue({ intent: 'intent-1', assertion: { id: 'cred' } });
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  globalThis.history.replaceState(null, '', '/');
});

describe('auth view-models', () => {
  it('reads the enrollment token from the fragment and wipes it', () => {
    globalThis.history.replaceState(null, '', '/enroll?x=1#AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcd');
    expect(readFragmentToken(globalThis.location, globalThis.history)).toBe('AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcd');
    expect(globalThis.location.hash).toBe('');
    expect(globalThis.location.pathname + globalThis.location.search).toBe('/enroll?x=1');
    globalThis.history.replaceState(null, '', '/enroll#<script>');
    expect(readFragmentToken(globalThis.location, globalThis.history)).toBeUndefined();
    expect(globalThis.location.hash).toBe('');
    expect(readFragmentToken(globalThis.location, globalThis.history)).toBeUndefined();
  });

  it('maps passkeys and errors', () => {
    const vm = toPasskeyVM({ id: 'r1', name: 'Phone', created_at: '2026-01-01T00:00:00Z', current: true });
    expect(vm).toEqual({ id: 'r1', name: 'Phone', createdAt: new Date('2026-01-01T00:00:00Z'), lastUsedAt: undefined, current: true });
    expect(authErrorKey(new ApiError(409, { error: 'last_passkey' }))).toBe('auth:errors.last_passkey');
    expect(authErrorKey(new ApiError(404, { error: 'not_found' }))).toBe('errors:not_found');
    expect(authErrorKey(new PasskeyError('cancelled'))).toBe('auth:passkey.cancelled');
    expect(authErrorKey(new PasskeyError('aborted'))).toBeUndefined();
    for (const k of ['auth:errors.last_passkey', 'auth:passkey.cancelled', 'auth:passkey.exists']) {
      expect(i18n.exists(k)).toBe(true);
    }
  });
});

describe('webauthn wrapper', () => {
  it('passes the publicKey options and classifies failures', async () => {
    await expect(getAssertion(loginOptions, true)).resolves.toBe(assertion);
    expect(wa.startAuthentication).toHaveBeenCalledWith({ optionsJSON: loginOptions.publicKey, useBrowserAutofill: true });
    await expect(createPasskey(createOptions)).resolves.toBe(attestation);
    expect(wa.startRegistration).toHaveBeenCalledWith({ optionsJSON: createOptions.publicKey });

    const cases: [unknown, string][] = [
      [Object.assign(new Error('x'), { name: 'NotAllowedError' }), 'cancelled'],
      [Object.assign(new Error('x'), { code: 'ERROR_CEREMONY_ABORTED' }), 'aborted'],
      [Object.assign(new Error('x'), { code: 'ERROR_AUTHENTICATOR_PREVIOUSLY_REGISTERED' }), 'exists'],
      [new Error('boom'), 'failed'],
    ];
    for (const [err, code] of cases) {
      wa.startRegistration.mockRejectedValueOnce(err);
      await expect(createPasskey(createOptions)).rejects.toMatchObject({ code });
    }
    await expect(getAssertion({ nope: true })).rejects.toMatchObject({ code: 'failed' });
    wa.supported = false;
    await expect(getAssertion(loginOptions)).rejects.toMatchObject({ code: 'unsupported' });
  });
});

describe('LoginPage', () => {
  it('signs in with the passkey prompt and uses the new session CSRF token', async () => {
    const calls = stubAPI(signedOut, (c) => {
      if (c.path === '/auth/login/begin') return json(200, { options: loginOptions, csrf: 'csrf-guest' });
      if (c.path === '/auth/login/finish') return json(200, signedIn);
      return undefined;
    });
    renderAt('/login', <LoginPage />);
    const input = await screen.findByLabelText(i18n.t('auth:login.handleLabel'));
    expect(input.getAttribute('autocomplete')).toBe('username webauthn');
    fireEvent.click(screen.getByTestId('login-passkey'));
    expect(await screen.findByText('home')).toBeTruthy();
    expect(wa.startAuthentication).toHaveBeenCalledWith({ optionsJSON: loginOptions.publicKey, useBrowserAutofill: false });
    const finish = calls.find((c) => c.path === '/auth/login/finish');
    expect(finish?.body).toEqual({ credential: assertion });
    expect(finish?.csrf).toBe('csrf-guest');
  });

  it('offers passkey autofill when the browser supports it', async () => {
    wa.autofill = true;
    let resolveAutofill: (v: unknown) => void = () => undefined;
    wa.startAuthentication.mockImplementationOnce(
      () =>
        new Promise((r) => {
          resolveAutofill = r;
        }),
    );
    stubAPI(signedOut, (c) => {
      if (c.path === '/auth/login/begin') return json(200, { options: loginOptions, csrf: 'csrf-guest' });
      if (c.path === '/auth/login/finish') return json(200, signedIn);
      return undefined;
    });
    renderAt('/login', <LoginPage />);
    await waitFor(() => {
      expect(wa.startAuthentication).toHaveBeenCalledWith({ optionsJSON: loginOptions.publicKey, useBrowserAutofill: true });
    });
    resolveAutofill(assertion);
    expect(await screen.findByText('home')).toBeTruthy();
  });

  it('shows a failed sign-in and stays on the page', async () => {
    stubAPI(signedOut, (c) => {
      if (c.path === '/auth/login/begin') return json(200, { options: loginOptions, csrf: 'csrf-guest' });
      return undefined; // finish: the uniform 404
    });
    renderAt('/login', <LoginPage />);
    fireEvent.click(await screen.findByTestId('login-passkey'));
    expect((await screen.findByRole('alert')).textContent).toBe(i18n.t('errors:not_found'));
    wa.startAuthentication.mockRejectedValueOnce(Object.assign(new Error('x'), { name: 'NotAllowedError' }));
    fireEvent.click(screen.getByTestId('login-passkey'));
    await waitFor(() => {
      expect(screen.getByRole('alert').textContent).toBe(i18n.t('auth:passkey.cancelled'));
    });
  });

  it('sends a signed-in visitor home', async () => {
    stubAPI(signedIn, () => undefined);
    renderAt('/login', <LoginPage />);
    expect(await screen.findByText('home')).toBeTruthy();
  });
});

const token = 'AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcd';

describe('EnrollPage', () => {
  it('enrolls a genesis account with terms and age, then offers a second passkey', async () => {
    globalThis.history.replaceState(null, '', `/enroll#${token}`);
    const calls = stubAPI(signedOut, (c) => {
      if (c.path === '/enroll/begin') {
        return json(200, { options: createOptions, purpose: 'genesis', csrf: 'csrf-enroll', tos_version: 3, min_age: 16 });
      }
      if (c.path === '/enroll/finish') return json(200, signedIn);
      return undefined;
    });
    renderAt('/enroll', <EnrollPage />);
    const create = await screen.findByTestId('enroll-create');
    expect(globalThis.location.hash).toBe('');
    expect(calls.find((c) => c.path === '/enroll/begin')?.body).toEqual({ token });
    expect(screen.getByText(i18n.t('auth:enroll.tos', { version: 3 }))).toBeTruthy();
    expect(create.hasAttribute('disabled')).toBe(true);
    fireEvent.click(screen.getByLabelText(i18n.t('auth:enroll.tos', { version: 3 })));
    expect(create.hasAttribute('disabled')).toBe(true);
    fireEvent.click(screen.getByLabelText(i18n.t('auth:enroll.age', { age: 16 })));
    expect(create.hasAttribute('disabled')).toBe(false);
    fireEvent.click(create);
    expect(await screen.findByTestId('second-add')).toBeTruthy();
    const finish = calls.find((c) => c.path === '/enroll/finish');
    expect(finish?.body).toEqual({ credential: attestation, remove_old: false, tos_version: 3, age_attested: true });
    expect(finish?.csrf).toBe('csrf-enroll');
    fireEvent.click(screen.getByTestId('second-done'));
    expect(await screen.findByText('home')).toBeTruthy();
  });

  it('recovers an account, offering to remove the old passkeys', async () => {
    globalThis.history.replaceState(null, '', `/enroll#${token}`);
    const calls = stubAPI(signedOut, (c) => {
      if (c.path === '/enroll/begin') {
        return json(200, {
          options: createOptions,
          purpose: 'recovery',
          csrf: 'csrf-enroll',
          existing_passkeys: [{ id: 'r1', name: 'Old phone', created_at: '2026-01-01T00:00:00Z', current: false }],
        });
      }
      if (c.path === '/enroll/finish') return json(200, signedIn);
      if (c.path === '/auth/passkeys/begin') return json(200, { options: createOptions });
      if (c.path === '/auth/passkeys/finish') return json(204, null);
      return undefined;
    });
    renderAt('/enroll', <EnrollPage />);
    expect(await screen.findByText(/Old phone/)).toBeTruthy();
    expect(screen.getByLabelText<HTMLInputElement>(i18n.t('auth:enroll.removeOld')).checked).toBe(true);
    fireEvent.click(screen.getByTestId('enroll-create'));
    fireEvent.click(await screen.findByTestId('second-add'));
    expect(await screen.findByText(i18n.t('auth:second.added'))).toBeTruthy();
    expect(calls.find((c) => c.path === '/enroll/finish')?.body).toEqual({
      credential: attestation,
      remove_old: true,
      tos_version: 0,
      age_attested: false,
    });
    expect(calls.find((c) => c.path === '/auth/passkeys/finish')?.body).toEqual({ credential: attestation, name: '' });
  });

  it('waits for the first session answer before the enrollment session exists (fresh-browser CSRF race)', async () => {
    // A fresh browser: the shell's first /api/session goes out without a cookie
    // and is answered late, after the page could already have begun.
    let answerSession: () => void = () => undefined;
    const calls: Call[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (req: Request) => {
        const path = new URL(req.url).pathname.replace(/^\/api/, '');
        const text = await req.text();
        calls.push({ method: req.method, path, body: text ? (JSON.parse(text) as unknown) : undefined, csrf: req.headers.get('X-PCC-CSRF') });
        if (path === '/session') {
          await new Promise<void>((r) => {
            answerSession = r;
          });
          return json(200, signedOut);
        }
        if (path === '/enroll/begin') return json(200, { options: createOptions, purpose: 'genesis', csrf: 'csrf-enroll', tos_version: 1, min_age: 16 });
        if (path === '/enroll/finish') return json(200, { ...signedIn, csrf: 'csrf-new' });
        return json(404, { error: 'not_found' });
      }),
    );
    globalThis.history.replaceState(null, '', '/enroll#AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcd');
    function Shell() {
      useSession();
      return null;
    }
    renderAt(
      '/enroll',
      <>
        <Shell />
        <EnrollPage />
      </>,
    );
    await waitFor(() => {
      expect(calls.some((c) => c.path === '/session')).toBe(true);
    });
    expect(calls.some((c) => c.path === '/enroll/begin')).toBe(false);
    answerSession();
    fireEvent.click(await screen.findByRole('checkbox', { name: /terms/i }));
    fireEvent.click(screen.getAllByRole('checkbox')[1] as HTMLElement);
    fireEvent.click(screen.getByTestId('enroll-create'));
    await waitFor(() => {
      expect(calls.some((c) => c.path === '/enroll/finish')).toBe(true);
    });
    // Every request after the enrollment session exists carries its CSRF token.
    const after = calls.slice(calls.findIndex((c) => c.path === '/enroll/begin') + 1).filter((c) => c.method === 'POST');
    expect(after.map((c) => [c.path, c.csrf])).toEqual([
      ['/enroll/begin', 'csrf-enroll'],
      ['/enroll/finish', 'csrf-enroll'],
    ]);
  });

  it('rejects a missing or dead link', async () => {
    globalThis.history.replaceState(null, '', `/enroll#${token}`);
    stubAPI(signedOut, () => undefined);
    renderAt('/enroll', <EnrollPage />);
    expect((await screen.findByRole('alert')).textContent).toBe(i18n.t('auth:enroll.invalid'));
  });
});

describe('PasskeysPage', () => {
  const one = [{ id: 'r1', name: 'Laptop', created_at: '2026-01-01T00:00:00Z', last_used_at: '2026-01-02T00:00:00Z', current: true }];
  const two = [...one, { id: 'r2', name: '', created_at: '2026-01-03T00:00:00Z', current: false }];

  it('never offers to remove the only passkey', async () => {
    stubAPI(signedIn, (c) => (c.path === '/auth/passkeys' ? json(200, { items: one }) : undefined));
    renderAt('/settings/passkeys', <PasskeysPage />);
    const btn = await screen.findByTestId('passkey-remove');
    expect(btn.hasAttribute('disabled')).toBe(true);
    expect(screen.getByText(i18n.t('auth:passkeys.lastHint'))).toBeTruthy();
    expect(screen.getByText(i18n.t('auth:passkeys.current'))).toBeTruthy();
  });

  it('removes a passkey after a step-up in a session not started with a passkey', async () => {
    vi.spyOn(globalThis, 'confirm').mockReturnValue(true);
    let fresh = false;
    const plain = two.map((p) => ({ ...p, current: false }));
    const calls = stubAPI(signedIn, (c) => {
      if (c.path === '/auth/passkeys') return json(200, { items: fresh ? plain.slice(0, 1) : plain });
      if (c.path === '/auth/passkeys/r2' && c.method === 'DELETE') {
        if (!fresh) return json(401, { error: 'step_up_required' });
        return json(204, null);
      }
      if (c.path === '/auth/stepup/begin') return json(200, { options: loginOptions });
      if (c.path === '/auth/stepup/finish') {
        fresh = true;
        return json(204, null);
      }
      return undefined;
    });
    renderAt('/settings/passkeys', <PasskeysPage />);
    const buttons = await screen.findAllByTestId('passkey-remove');
    expect(buttons).toHaveLength(2);
    fireEvent.click(buttons[1] as HTMLElement);
    fireEvent.click(await screen.findByTestId('stepup-verify'));
    expect(await screen.findByText(i18n.t('auth:passkeys.removed'))).toBeTruthy();
    expect(calls.filter((c) => c.method === 'DELETE')).toHaveLength(2);
    expect(calls.find((c) => c.path === '/auth/stepup/finish')?.body).toEqual({ credential: assertion });
    expect(screen.queryByTestId('stepup-verify')).toBeNull();
    expect(cosign.requestCosign).not.toHaveBeenCalled();
  });

  it('co-signs a removal with an existing passkey in a passkey session', async () => {
    vi.spyOn(globalThis, 'confirm').mockReturnValue(true);
    let removed = false;
    const calls = stubAPI(signedIn, (c) => {
      if (c.path === '/auth/passkeys') return json(200, { items: removed ? one : two });
      if (c.path === '/auth/passkeys/r2' && c.method === 'DELETE') {
        removed = true;
        return json(204, null);
      }
      return undefined;
    });
    renderAt('/settings/passkeys', <PasskeysPage />);
    const buttons = await screen.findAllByTestId('passkey-remove');
    fireEvent.click(buttons[1] as HTMLElement);
    expect(await screen.findByText(i18n.t('auth:passkeys.removed'))).toBeTruthy();
    expect(cosign.requestCosign).toHaveBeenCalledWith({
      purpose: 'passkey_remove',
      target: 'r2',
      summary: i18n.t('auth:cosign.removeSummary', { name: i18n.t('auth:passkeys.unnamed') }),
    });
    expect(calls.find((c) => c.method === 'DELETE')?.body).toEqual({ intent: 'intent-1', assertion: { id: 'cred' } });
    expect(screen.queryByTestId('stepup-verify')).toBeNull();
  });

  it('cancelling the co-signature removes nothing and shows no error', async () => {
    vi.spyOn(globalThis, 'confirm').mockReturnValue(true);
    cosign.requestCosign.mockResolvedValue(null);
    const calls = stubAPI(signedIn, (c) => (c.path === '/auth/passkeys' ? json(200, { items: two }) : undefined));
    renderAt('/settings/passkeys', <PasskeysPage />);
    fireEvent.click((await screen.findAllByTestId('passkey-remove'))[1] as HTMLElement);
    await waitFor(() => {
      expect(cosign.requestCosign).toHaveBeenCalled();
    });
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('a parked new passkey is stored once an existing one co-signs it', async () => {
    const calls = stubAPI(signedIn, (c) => {
      if (c.path === '/auth/passkeys') return json(200, { items: one });
      if (c.path === '/auth/passkeys/begin') return json(200, { options: createOptions });
      if (c.path === '/auth/passkeys/finish') {
        return (c.body as { intent?: string }).intent ? json(204, null) : json(202, { cosign: 'passkey_add' });
      }
      return undefined;
    });
    renderAt('/settings/passkeys', <PasskeysPage />);
    fireEvent.change(await screen.findByLabelText(i18n.t('auth:passkeys.nameLabel')), { target: { value: 'Phone' } });
    fireEvent.click(screen.getByTestId('passkey-add'));
    expect(await screen.findByText(i18n.t('auth:passkeys.added'))).toBeTruthy();
    expect(cosign.requestCosign).toHaveBeenCalledWith({ purpose: 'passkey_add', summary: i18n.t('auth:cosign.addSummary', { name: 'Phone' }) });
    const finishes = calls.filter((c) => c.path === '/auth/passkeys/finish');
    expect(finishes.map((c) => c.body)).toEqual([
      { credential: attestation, name: 'Phone' },
      { intent: 'intent-1', assertion: { id: 'cred' } },
    ]);
  });

  it('adds a passkey and explains why the same device is refused', async () => {
    const calls = stubAPI(signedIn, (c) => {
      if (c.path === '/auth/passkeys') return json(200, { items: one });
      if (c.path === '/auth/passkeys/begin') return json(200, { options: createOptions });
      if (c.path === '/auth/passkeys/finish') return json(204, null);
      return undefined;
    });
    renderAt('/settings/passkeys', <PasskeysPage />);
    fireEvent.change(await screen.findByLabelText(i18n.t('auth:passkeys.nameLabel')), { target: { value: 'Phone' } });
    fireEvent.click(screen.getByTestId('passkey-add'));
    expect(await screen.findByText(i18n.t('auth:passkeys.added'))).toBeTruthy();
    expect(calls.find((c) => c.path === '/auth/passkeys/finish')?.body).toEqual({ credential: attestation, name: 'Phone' });
    wa.startRegistration.mockRejectedValueOnce(Object.assign(new Error('x'), { name: 'InvalidStateError' }));
    fireEvent.click(screen.getByTestId('passkey-add'));
    expect((await screen.findByRole('alert')).textContent).toBe(i18n.t('auth:passkey.exists'));
  });

  it('restarts the whole add flow when finish asks for a step-up', async () => {
    // Server model: one ceremony per session; the step-up replaces the pending
    // add ceremony, so a finish retried without a new begin would be rejected.
    let fresh = false;
    let ceremony: 'add' | 'stepup' | undefined;
    const calls = stubAPI(signedIn, (c) => {
      if (c.path === '/auth/passkeys') return json(200, { items: one });
      if (c.path === '/auth/passkeys/begin') {
        ceremony = 'add';
        return json(200, { options: createOptions });
      }
      if (c.path === '/auth/passkeys/finish') {
        if (!fresh) return json(401, { error: 'step_up_required' });
        const ok = ceremony === 'add';
        ceremony = undefined;
        return ok ? json(204, null) : json(400, { error: 'passkey_rejected' });
      }
      if (c.path === '/auth/stepup/begin') {
        ceremony = 'stepup';
        return json(200, { options: loginOptions });
      }
      if (c.path === '/auth/stepup/finish') {
        ceremony = undefined;
        fresh = true;
        return json(204, null);
      }
      return undefined;
    });
    renderAt('/settings/passkeys', <PasskeysPage />);
    fireEvent.click(await screen.findByTestId('passkey-add'));
    fireEvent.click(await screen.findByTestId('stepup-verify'));
    expect(await screen.findByText(i18n.t('auth:passkeys.added'))).toBeTruthy();
    expect(calls.filter((c) => c.path === '/auth/passkeys/begin')).toHaveLength(2);
    expect(calls.filter((c) => c.path === '/auth/passkeys/finish')).toHaveLength(2);
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('signs out and points a signed-out visitor to the login page', async () => {
    const calls = stubAPI(signedIn, (c) => {
      if (c.path === '/auth/passkeys') return json(200, { items: one });
      if (c.path === '/auth/logout') return json(204, null);
      return undefined;
    });
    renderAt('/settings/passkeys', <PasskeysPage />);
    fireEvent.click(await screen.findByTestId('sign-out'));
    expect(await screen.findByText('home')).toBeTruthy();
    expect(calls.some((c) => c.path === '/auth/logout' && c.csrf === 'csrf-user')).toBe(true);
    vi.unstubAllGlobals();
    stubAPI(signedOut, () => undefined);
    renderAt('/settings/passkeys', <PasskeysPage />);
    fireEvent.click(await screen.findByTestId('passkeys-login'));
    expect(await screen.findByText('login page')).toBeTruthy();
  });
});
