// Step-up: the shared request store and the global modal.
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from '../../api/client';
import { ApiError } from '../../api/errors';
import { i18n } from '../../i18n/setup';
import { StepUpModal } from './StepUpModal';
import { requestStepUp, settleStepUp, withStepUp } from './store';

const wa = vi.hoisted(() => ({ startAuthentication: vi.fn(), cancelCeremony: vi.fn() }));

vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: () => true,
  browserSupportsWebAuthnAutofill: () => Promise.resolve(false),
  startAuthentication: wa.startAuthentication,
  startRegistration: vi.fn(),
  WebAuthnAbortService: { cancelCeremony: wa.cancelCeremony, createNewAbortSignal: () => new AbortController().signal },
}));

const options = { publicKey: { challenge: 'Y2hh', allowCredentials: [{ id: 'own', type: 'public-key' }] } };
const assertion = { id: 'own', rawId: 'own', type: 'public-key', response: {}, clientExtensionResults: {} };

function json(status: number, body: unknown): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function stubFinish(finish: () => Response): string[] {
  const paths: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn((req: Request) => {
      const path = new URL(req.url).pathname.replace(/^\/api/, '');
      paths.push(path);
      if (path === '/auth/stepup/begin') return Promise.resolve(json(200, { options }));
      if (path === '/auth/stepup/finish') return Promise.resolve(finish());
      if (path === '/me/export') return Promise.resolve(json(401, { error: 'step_up_required' }));
      return Promise.resolve(json(404, { error: 'not_found' }));
    }),
  );
  return paths;
}

beforeEach(() => {
  wa.startAuthentication.mockReset().mockResolvedValue(assertion);
  wa.cancelCeremony.mockReset();
});

afterEach(() => {
  act(() => {
    settleStepUp(false);
  });
  vi.unstubAllGlobals();
});

describe('step-up store', () => {
  it('shares one pending request and retries once after a verified step-up', async () => {
    const a = requestStepUp();
    const b = requestStepUp();
    expect(a).toBe(b);
    settleStepUp(true);
    await expect(a).resolves.toBe(true);

    let calls = 0;
    const fn = () => {
      calls++;
      return calls === 1 ? Promise.reject(new ApiError(401, { error: 'step_up_required' })) : Promise.resolve('ok');
    };
    const run = withStepUp(fn);
    await Promise.resolve();
    settleStepUp(true);
    await expect(run).resolves.toBe('ok');
    expect(calls).toBe(2);

    calls = 0;
    const cancelled = withStepUp(fn);
    await Promise.resolve();
    settleStepUp(false);
    await expect(cancelled).rejects.toMatchObject({ code: 'step_up_required' });
    await expect(withStepUp(() => Promise.reject(new ApiError(404, {})))).rejects.toMatchObject({ status: 404 });
  });
});

describe('StepUpModal', () => {
  it('opens on step_up_required from any API call and verifies with an own passkey', async () => {
    const paths = stubFinish(() => json(204, null));
    render(<StepUpModal />);
    expect(screen.queryByRole('dialog')).toBeNull();
    await api.POST('/me/export', { body: {} });
    const dialog = await screen.findByRole('dialog');
    expect(dialog.getAttribute('aria-modal')).toBe('true');
    expect(screen.getByText(i18n.t('stepup:title'))).toBeTruthy();
    const pending = requestStepUp();
    fireEvent.click(screen.getByTestId('stepup-verify'));
    await expect(pending).resolves.toBe(true);
    expect(wa.startAuthentication).toHaveBeenCalledWith({ optionsJSON: options.publicKey, useBrowserAutofill: false });
    expect(paths).toContain('/auth/stepup/finish');
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).toBeNull();
    });
  });

  it('shows a rejected passkey and can be cancelled', async () => {
    stubFinish(() => json(400, { error: 'passkey_rejected' }));
    render(<StepUpModal />);
    let pending: Promise<boolean> = Promise.resolve(true);
    act(() => {
      pending = requestStepUp();
    });
    fireEvent.click(await screen.findByTestId('stepup-verify'));
    expect((await screen.findByRole('alert')).textContent).toBe(i18n.t('auth:errors.passkey_rejected'));
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    await expect(pending).resolves.toBe(false);
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});
