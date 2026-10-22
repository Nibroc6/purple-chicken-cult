// Co-sign: the record view-model, the request store and the global dialog.
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { i18n } from '../../i18n/setup';
import { CosignDialog } from './CosignDialog';
import { requestCosign, settleCosign } from './store';
import { toRecordVM, toRecordVMs } from './vm';

const wa = vi.hoisted(() => ({ startAuthentication: vi.fn(), cancelCeremony: vi.fn() }));

vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: () => true,
  browserSupportsWebAuthnAutofill: () => Promise.resolve(false),
  startAuthentication: wa.startAuthentication,
  startRegistration: vi.fn(),
  WebAuthnAbortService: { cancelCeremony: wa.cancelCeremony, createNewAbortSignal: () => new AbortController().signal },
}));

const record = {
  a: '0190a0a0-0000-7000-8000-000000000001',
  b: '0190a0a0-0000-7000-8000-000000000002',
  cosign: { issued_at: '2026-01-01T00:00:00.000Z', nonce: '0190a0a0-0000-7000-8000-0000000000ff' },
  created_at: '2026-01-01T00:00:00.000Z',
  kind: 'friendship',
  level: 'friend',
  schema_version: 1,
};
const options = { publicKey: { challenge: 'Y2hh', allowCredentials: [{ id: 'own', type: 'public-key' }] } };
const assertion = { id: 'own', rawId: 'own', type: 'public-key', response: {}, clientExtensionResults: {} };

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

describe('record view-model', () => {
  it('keeps every record field and separates the envelope and co-sign members', () => {
    const vm = toRecordVM(record);
    expect(vm.kind).toBe('friendship');
    expect(vm.fields.map((f) => f.name)).toEqual(['a', 'b', 'created_at', 'level']);
    expect(vm.fields.every((f) => f.known)).toBe(true);
    expect(vm.nonce).toBe(record.cosign.nonce);
    expect(vm.issuedAt?.toISOString()).toBe('2026-01-01T00:00:00.000Z');
    expect(JSON.parse(vm.exact)).toEqual(record);
  });

  it('shows unknown kinds and fields verbatim and maps signup bundles', () => {
    const vm = toRecordVM({ kind: 'future', custom: { x: 1 } });
    expect(vm.kind).toBeUndefined();
    expect(vm.rawKind).toBe('future');
    expect(vm.fields).toEqual([{ name: 'custom', known: undefined, value: '{"x":1}' }]);
    expect(toRecordVMs([record, { ...record, kind: 'invite' }]).map((r) => r.kind)).toEqual(['friendship', 'invite']);
    expect(() => toRecordVM('nope')).toThrow();
  });
});

describe('cosign store', () => {
  it('a new request cancels the pending one', async () => {
    const first = requestCosign({ purpose: 'block', target: 'r1' });
    const second = requestCosign({ purpose: 'unfriend', target: 'r2' });
    await expect(first).resolves.toBeNull();
    settleCosign({ intent: 'i', assertion: {} });
    await expect(second).resolves.toEqual({ intent: 'i', assertion: {} });
  });
});

describe('CosignDialog', () => {
  it('shows the exact record and resolves with the intent and the assertion', async () => {
    const bodies = stubBegin(() => json(200, { intent: 'intent-ref', record, options }));
    render(<CosignDialog />);
    let result: Promise<unknown> = Promise.resolve();
    act(() => {
      result = requestCosign({ purpose: 'friend_accept', target: 'person-ref', summary: 'Accept Bob' });
    });
    expect(await screen.findByRole('heading', { name: i18n.t('cosign:kinds.friendship') })).toBeTruthy();
    expect(bodies).toEqual([{ purpose: 'friend_accept', target: 'person-ref' }]);
    expect(screen.getByText('Accept Bob')).toBeTruthy();
    expect(screen.getByText(i18n.t('cosign:fields.level'))).toBeTruthy();
    expect(screen.getByText(record.a)).toBeTruthy();
    expect(screen.queryByText(record.cosign.nonce)).toBeNull(); // only inside the exact JSON
    expect(screen.getByTestId('cosign-exact').textContent).toContain(record.cosign.nonce);
    fireEvent.click(screen.getByTestId('cosign-sign'));
    await expect(result).resolves.toEqual({ intent: 'intent-ref', assertion });
    expect(wa.startAuthentication).toHaveBeenCalledWith(
      expect.objectContaining({ optionsJSON: options.publicKey }) as unknown,
    );
    await waitFor(() => {
      expect(screen.queryByTestId('cosign-dialog')).toBeNull();
    });
  });

  it('reports a refused intent and resolves null on cancel', async () => {
    stubBegin(() => json(404, { error: 'not_found' }));
    render(<CosignDialog />);
    let result: Promise<unknown> = Promise.resolve();
    act(() => {
      result = requestCosign({ purpose: 'nope' });
    });
    expect((await screen.findByRole('alert')).textContent).toBe(i18n.t('errors:not_found'));
    expect(screen.queryByTestId('cosign-sign')).toBeNull();
    fireEvent.click(screen.getByTestId('cosign-cancel'));
    await expect(result).resolves.toBeNull();
  });

  it('fills in the wait of a rate-limited intent', async () => {
    stubBegin(() => json(429, { error: 'rate_limited', retry_after_s: 4 })); // past the quiet wait
    render(<CosignDialog />);
    act(() => {
      void requestCosign({ purpose: 'passkey_add' });
    });
    const text = (await screen.findByRole('alert')).textContent;
    expect(text).toBe(i18n.t('errors:rate_limited', { seconds: 4 }));
    expect(text).toContain('4 seconds');
    fireEvent.click(screen.getByTestId('cosign-cancel'));
  });

  it('keeps the record after a cancelled passkey prompt so the person can retry', async () => {
    stubBegin(() => json(200, { intent: 'intent-ref', record, options }));
    wa.startAuthentication.mockRejectedValueOnce(Object.assign(new Error('x'), { name: 'NotAllowedError' }));
    render(<CosignDialog />);
    let result: Promise<unknown> = Promise.resolve();
    act(() => {
      result = requestCosign({ purpose: 'block', target: 't' });
    });
    fireEvent.click(await screen.findByTestId('cosign-sign'));
    expect((await screen.findByRole('alert')).textContent).toBe(i18n.t('auth:passkey.cancelled'));
    fireEvent.click(screen.getByTestId('cosign-sign'));
    await expect(result).resolves.toEqual({ intent: 'intent-ref', assertion });
  });

  it('Escape cancels', async () => {
    stubBegin(() => json(200, { intent: 'intent-ref', record, options }));
    render(<CosignDialog />);
    let result: Promise<unknown> = Promise.resolve();
    act(() => {
      result = requestCosign({ purpose: 'block' });
    });
    fireEvent.keyDown(await screen.findByTestId('cosign-dialog'), { key: 'Escape' });
    await expect(result).resolves.toBeNull();
  });
});
