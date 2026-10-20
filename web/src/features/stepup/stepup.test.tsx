// Step-up: the shared request store and the global modal.
import { QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from '../../api/client';
import { ApiError } from '../../api/errors';
import { dispatch, resetSSE } from '../../api/sse';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import { cdpHint, startSignals, type SignalsReport } from './signals';
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

function renderModal() {
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <StepUpModal />
    </QueryClientProvider>,
  );
}

describe('StepUpModal', () => {
  it('opens on step_up_required from any API call and verifies with an own passkey', async () => {
    const paths = stubFinish(() => json(204, null));
    renderModal();
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
    renderModal();
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

describe('signal-triggered step-up', () => {
  afterEach(() => {
    resetSSE();
  });

  it('explains a step-up the automation signals asked for, then reloads the page data', async () => {
    let pendingStepUp = true;
    vi.stubGlobal(
      'fetch',
      vi.fn((req: Request) => {
        const path = new URL(req.url).pathname.replace(/^\/api/, '');
        if (path === '/session') return Promise.resolve(json(200, { authenticated: true, kind: 'user', csrf: 'c', step_up: pendingStepUp }));
        if (path === '/auth/stepup/begin') return Promise.resolve(json(200, { options }));
        if (path === '/auth/stepup/finish') {
          pendingStepUp = false;
          return Promise.resolve(json(204, null));
        }
        if (path === '/feed') return Promise.resolve(json(401, { error: 'step_up_required' }));
        return Promise.resolve(json(404, { error: 'not_found' }));
      }),
    );
    renderModal();
    await api.GET('/feed', { params: { query: {} } });
    const dialog = await screen.findByRole('dialog');
    await waitFor(() => {
      expect(dialog.getAttribute('data-reason')).toBe('signals');
    });
    expect(screen.getByText(i18n.t('signals:stepup.title'))).toBeTruthy();
    expect(screen.getByText(i18n.t('signals:stepup.privacy'))).toBeTruthy();
    fireEvent.click(screen.getByTestId('stepup-verify'));
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).toBeNull();
    });
  });

  it('a fresh-check 401 keeps the plain copy', async () => {
    stubFinish(() => json(204, null));
    renderModal();
    await api.POST('/me/export', { body: {} });
    const dialog = await screen.findByRole('dialog');
    expect(dialog.getAttribute('data-reason')).toBe('fresh');
    expect(screen.getByText(i18n.t('stepup:title'))).toBeTruthy();
  });

  it('opens on the stepup event of the stream', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(json(200, { authenticated: true, kind: 'user', csrf: 'c', step_up: true }))),
    );
    vi.stubGlobal(
      'EventSource',
      class {
        addEventListener() {
          return undefined;
        }
        close() {
          return undefined;
        }
      },
    );
    renderModal();
    await waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalled();
    });
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
      dispatch('stepup', {});
    });
    expect((await screen.findByRole('dialog')).getAttribute('data-reason')).toBe('signals');
  });
});

describe('client signals', () => {
  function fakeEnv() {
    let t = 0;
    let visible = true;
    let tick: () => void = () => undefined;
    const target = new EventTarget();
    const sent: SignalsReport[] = [];
    const env = {
      now: () => t,
      visible: () => visible,
      webdriver: () => true,
      cdp: () => false,
      target,
      send: (r: SignalsReport) => {
        sent.push(r);
        return Promise.resolve();
      },
      setInterval: (fn: () => void) => {
        tick = fn;
        return 1;
      },
      clearInterval: () => undefined,
    };
    return {
      env,
      sent,
      advance: (ms: number) => {
        t += ms;
      },
      hide: () => {
        visible = false;
        target.dispatchEvent(new Event('visibilitychange'));
      },
      show: () => {
        visible = true;
        target.dispatchEvent(new Event('visibilitychange'));
      },
      tick: () => {
        tick();
      },
    };
  }

  it('reports input counts per minute and the visible time — counts only', () => {
    const f = fakeEnv();
    const stop = startSignals(f.env);
    f.env.target.dispatchEvent(new Event('keydown'));
    f.env.target.dispatchEvent(new Event('keydown'));
    f.advance(61_000);
    f.env.target.dispatchEvent(new Event('wheel'));
    f.env.target.dispatchEvent(new Event('mousemove')); // never counted
    f.advance(30_000);
    f.tick();
    expect(f.sent).toEqual([{ webdriver: true, cdp: false, inputs_per_min: [2, 1], visible_ms: 91_000 }]);
    expect(Object.keys(f.sent[0] ?? {}).sort()).toEqual(['cdp', 'inputs_per_min', 'visible_ms', 'webdriver']);
    // Hidden time does not count; hiding flushes.
    f.advance(10_000);
    f.hide();
    expect(f.sent[1]?.visible_ms).toBe(10_000);
    f.advance(600_000);
    f.tick(); // not visible: no report
    expect(f.sent).toHaveLength(2);
    f.show();
    f.advance(5_000);
    stop();
    expect(f.sent[2]?.visible_ms).toBe(5_000);
  });

  it('sends nothing for an empty period', () => {
    const f = fakeEnv();
    const stop = startSignals({ ...f.env, visible: () => false });
    f.tick();
    stop();
    expect(f.sent).toHaveLength(0);
  });

  it('hints at automation drivers from the globals they leave', () => {
    expect(cdpHint({})).toBe(false);
    expect(cdpHint({ cdc_adoQpoasnfa76pfcZLmcfl_Array: [] })).toBe(true);
    expect(cdpHint({ __playwright__binding__: () => undefined })).toBe(true);
  });
});
