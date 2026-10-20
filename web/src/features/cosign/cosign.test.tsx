// Co-signing: the shared request store and the global dialog.
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { i18n } from '../../i18n/setup';
import { CosignDialog } from './CosignDialog';
import { settleCosign } from './store';
import { requestCosign, type CosignResult } from './index';

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
const record = {
  a: '0190a5c4-0000-7000-8000-000000000001',
  b: '0190a5c4-0000-7000-8000-000000000002',
  created_at: '2026-10-20T21:00:00.000Z',
  issued_at: '2026-10-20T21:00:00.000Z',
  kind: 'friendship',
  level: 'friend',
  nonce: '6f1c2a5e-1111-4222-8333-444455556666',
  schema_version: 1,
};

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

function stubBegin(answer: () => Response): unknown[] {
  const bodies: unknown[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname.replace(/^\/api/, '');
      if (path === '/cosign') {
        bodies.push(await req.json());
        return answer();
      }
      return json(404, { error: 'not_found' });
    }),
  );
  return bodies;
}

beforeEach(() => {
  wa.startAuthentication.mockReset().mockResolvedValue(assertion);
  wa.cancelCeremony.mockReset();
});

afterEach(() => {
  act(() => {
    settleCosign(null);
  });
  vi.unstubAllGlobals();
});

describe('cosign store', () => {
  it('shares a pending request for the same change and queues a different one', async () => {
    const a = requestCosign({ purpose: 'block', target: 'r1' });
    const b = requestCosign({ purpose: 'block', target: 'r1' });
    const c = requestCosign({ purpose: 'unfriend', target: 'r1' });
    const done: CosignResult = { intent: 'i', assertion: {} };
    settleCosign(done);
    await expect(a).resolves.toBe(done);
    await expect(b).resolves.toBe(done);
    await Promise.resolve();
    settleCosign(null);
    await expect(c).resolves.toBeNull();
  });
});

describe('CosignDialog', () => {
  it('shows the record being signed and signs it with an own passkey', async () => {
    const bodies = stubBegin(() => json(200, { intent: 'intent-ref', record, options }));
    render(<CosignDialog />);
    expect(screen.queryByRole('dialog')).toBeNull();
    let pending: Promise<CosignResult | null> = Promise.resolve(null);
    act(() => {
      pending = requestCosign({ purpose: 'friend_accept', target: 'bob-ref' });
    });
    const dialog = await screen.findByRole('dialog');
    expect(dialog.getAttribute('aria-modal')).toBe('true');
    expect(screen.getByText(i18n.t('cosign:purpose.friend_accept'))).toBeTruthy();
    const shown = await screen.findByTestId('cosign-record');
    expect(shown.querySelector('code')?.textContent).toContain('"nonce": "6f1c2a5e-1111-4222-8333-444455556666"');
    expect(shown.querySelector('time')?.getAttribute('dateTime')).toBe('2026-10-20T21:00:00.000Z');
    expect(bodies).toEqual([{ purpose: 'friend_accept', target: 'bob-ref' }]);
    fireEvent.click(screen.getByTestId('cosign-sign'));
    await expect(pending).resolves.toEqual({ intent: 'intent-ref', assertion });
    expect(wa.startAuthentication).toHaveBeenCalledWith({ optionsJSON: options.publicKey, useBrowserAutofill: false });
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).toBeNull();
    });
  });

  it('shows why nothing can be signed and can be cancelled', async () => {
    stubBegin(() => json(404, { error: 'not_found' }));
    render(<CosignDialog />);
    let pending: Promise<CosignResult | null> = Promise.resolve(null);
    act(() => {
      pending = requestCosign({ purpose: 'block' });
    });
    expect((await screen.findByRole('alert')).textContent).toBe(i18n.t('errors:not_found'));
    expect(screen.getByTestId<HTMLButtonElement>('cosign-sign').disabled).toBe(true);
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    await expect(pending).resolves.toBeNull();
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('keeps the dialog open when the passkey prompt is dismissed', async () => {
    stubBegin(() => json(200, { intent: 'intent-ref', record, options }));
    wa.startAuthentication.mockRejectedValue(Object.assign(new Error('x'), { name: 'NotAllowedError' }));
    render(<CosignDialog />);
    act(() => {
      void requestCosign({ purpose: 'unfriend', target: 'r' });
    });
    await screen.findByTestId('cosign-record');
    fireEvent.click(screen.getByTestId('cosign-sign'));
    expect((await screen.findByRole('alert')).textContent).toBe(i18n.t('auth:passkey.cancelled'));
    expect(screen.getByRole('dialog')).toBeTruthy();
  });
});
