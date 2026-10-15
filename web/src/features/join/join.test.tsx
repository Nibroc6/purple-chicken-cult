// Join feature: QR decoding, view-models and the join flow against a stubbed
// API, a stubbed camera scanner and a stubbed passkey prompt.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import QRCode from 'qrcode';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/errors';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import { PasskeyError } from '../auth/webauthn';
import { decodeFrame, isInviteFrame } from './decode';
import JoinPage from './JoinPage';
import { browserLanguages, joinErrorKey } from './vm';

const FRAME = 'pcc1.AAAAAAAAAAAAAAAAAAAAAA.177724320.BBBBBBBBBBBBBB';

const cam = vi.hoisted(() => ({
  frames: [] as string[],
  fail: false,
  stop: vi.fn(),
}));

vi.mock('./scanner', () => {
  class CameraError extends Error {}
  return {
    CameraError,
    startScanner: (_video: HTMLVideoElement, onFrame: (p: string) => void) => {
      if (cam.fail) return Promise.reject(new CameraError());
      // One decoded frame every 50 ms, like the camera loop.
      cam.frames.forEach((f, i) => {
        setTimeout(() => {
          onFrame(f);
        }, 50 * i);
      });
      return Promise.resolve({ stop: cam.stop });
    },
  };
});

const pk = vi.hoisted(() => ({ create: vi.fn() }));
vi.mock('../auth/webauthn', async (orig) => ({
  ...(await orig<typeof import('../auth/webauthn')>()),
  createPasskey: pk.create,
}));

interface Call {
  method: string;
  path: string;
  body: unknown;
}

function json(status: number, body: unknown): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function stubAPI(session: unknown, handler: (c: Call) => Response | undefined): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const text = await req.text();
      const c: Call = {
        method: req.method,
        path: new URL(req.url).pathname.replace(/^\/api/, ''),
        body: text ? (JSON.parse(text) as unknown) : undefined,
      };
      calls.push(c);
      if (c.path === '/session') return json(200, session);
      return handler(c) ?? json(404, { error: 'not_found' });
    }),
  );
  return calls;
}

function renderJoin() {
  const router = createMemoryRouter(
    [
      { path: '/join', element: <JoinPage /> },
      { path: '/', element: <p>home</p> },
    ],
    { initialEntries: ['/join'] },
  );
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

// qrImage renders text as an RGBA QR image (4 px per module, quiet zone).
function qrImage(text: string): { data: Uint8ClampedArray; size: number } {
  const q = QRCode.create(text, { errorCorrectionLevel: 'M' });
  const scale = 4;
  const n = q.modules.size;
  const size = (n + 8) * scale;
  const data = new Uint8ClampedArray(size * size * 4).fill(255);
  for (let y = 0; y < n; y++) {
    for (let x = 0; x < n; x++) {
      if (!q.modules.get(y, x)) continue;
      for (let dy = 0; dy < scale; dy++) {
        for (let dx = 0; dx < scale; dx++) {
          const i = (((y + 4) * scale + dy) * size + (x + 4) * scale + dx) * 4;
          data[i] = data[i + 1] = data[i + 2] = 0;
        }
      }
    }
  }
  return { data, size };
}

afterEach(() => {
  vi.unstubAllGlobals();
  cam.frames = [];
  cam.fail = false;
  cam.stop.mockClear();
  pk.create.mockReset();
});

describe('QR decoding', () => {
  it('decodes an invite frame and ignores other codes', () => {
    const img = qrImage(FRAME);
    expect(decodeFrame(img.data, img.size, img.size)).toBe(FRAME);
    const other = qrImage('https://example.com/');
    expect(decodeFrame(other.data, other.size, other.size)).toBeNull();
    const blank = new Uint8ClampedArray(64 * 64 * 4).fill(255);
    expect(decodeFrame(blank, 64, 64)).toBeNull();
  });

  it('recognizes only P0 frames', () => {
    expect(isInviteFrame(FRAME)).toBe(true);
    expect(isInviteFrame(`${FRAME}x`)).toBe(false);
    expect(isInviteFrame('pcc2.AAAAAAAAAAAAAAAAAAAAAA.1.BBBBBBBBBBBBBB')).toBe(false);
  });
});

describe('join view-models', () => {
  it('maps errors to the join, auth and shared catalogs', () => {
    expect(joinErrorKey(new ApiError(409, { error: 'handle_taken' }))).toBe('join:errors.handle_taken');
    expect(joinErrorKey(new ApiError(404, { error: 'not_found' }))).toBe('errors:not_found');
    expect(joinErrorKey(new PasskeyError('cancelled'))).toBe('auth:passkey.cancelled');
    expect(joinErrorKey(new PasskeyError('aborted'))).toBeUndefined();
  });

  it('derives reading languages from the browser', () => {
    expect(browserLanguages(['de-CH', 'de', 'en-US', 'x-klingon'])).toEqual(['de', 'en']);
    expect(browserLanguages([])).toEqual(['en']);
  });
});

describe('join page', () => {
  const start = { csrf: 'csrf-guest', tos_version: 3, min_age: 16 };

  it('scans, shows the SAS, waits for the inviter and registers', async () => {
    let state = 'claimed';
    cam.frames = ['pcc1.stale', FRAME];
    pk.create.mockResolvedValue({ id: 'cred' });
    const calls = stubAPI({ authenticated: false, kind: 'none' }, (c) => {
      if (c.path === '/join/start') return json(200, start);
      if (c.path === '/join/claim') {
        return (c.body as { payload: string }).payload === FRAME ? json(204, null) : json(404, { error: 'not_found' });
      }
      if (c.path === '/join/status') return json(200, { state, sas: '424242' });
      if (c.path === '/join/register/begin') {
        const b = c.body as { handle: string };
        return b.handle === 'taken'
          ? json(409, { error: 'handle_taken', field: 'handle' })
          : json(200, { options: { publicKey: { challenge: 'x' } } });
      }
      if (c.path === '/join/register/finish') {
        return json(200, {
          authenticated: true,
          kind: 'user',
          csrf: 'csrf-user',
          me: { handle: 'newbie', inviter: { kind: 'user', handle: 'alice' } },
        });
      }
      return undefined;
    });
    renderJoin();
    fireEvent.click(await screen.findByTestId('join-scan'));
    expect((await screen.findByTestId('join-sas')).textContent).toBe('424242');
    expect(cam.stop).toHaveBeenCalled();
    state = 'confirmed';

    const submit = await screen.findByTestId('join-submit', {}, { timeout: 3000 });
    expect((submit as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText(tr('join:form.handle')), { target: { value: 'taken' } });
    fireEvent.click(screen.getByLabelText(tr('join:form.tos', { version: 3 })));
    fireEvent.click(screen.getByLabelText(tr('join:form.age', { age: 16 })));
    fireEvent.click(submit);
    expect(await screen.findByText(tr('join:errors.handle_taken'))).toBeTruthy();

    fireEvent.change(screen.getByLabelText(tr('join:form.handle')), { target: { value: ' newbie ' } });
    fireEvent.click(screen.getByTestId('join-submit'));
    expect((await screen.findByTestId('join-invited-by')).textContent).toBe(tr('join:done.invitedBy', { handle: 'alice' }));
    expect(screen.getByRole('heading', { name: tr('auth:second.title') })).toBeTruthy();

    const begin = calls.filter((c) => c.path === '/join/register/begin').map((c) => c.body);
    expect(begin[1]).toMatchObject({ handle: 'newbie', tos_version: 3, age_attested: true });
    expect(calls.filter((c) => c.path === '/join/claim').map((c) => (c.body as { payload: string }).payload)).toEqual([
      'pcc1.stale',
      FRAME,
    ]);
    expect(calls.find((c) => c.path === '/join/register/finish')?.body).toEqual({ credential: { id: 'cred' } });
  });

  it('ends when the inviter cancels', async () => {
    cam.frames = [FRAME];
    stubAPI({ authenticated: false, kind: 'none' }, (c) => {
      if (c.path === '/join/start') return json(200, start);
      if (c.path === '/join/claim') return json(204, null);
      if (c.path === '/join/status') return json(200, { state: 'cancelled' });
      return undefined;
    });
    renderJoin();
    fireEvent.click(await screen.findByTestId('join-scan'));
    expect((await screen.findByTestId('join-ended')).textContent).toBe(tr('join:ended.cancelled'));
  });

  it('explains a missing camera', async () => {
    cam.fail = true;
    stubAPI({ authenticated: false, kind: 'none' }, (c) => (c.path === '/join/start' ? json(200, start) : undefined));
    renderJoin();
    fireEvent.click(await screen.findByTestId('join-scan'));
    expect(await screen.findByText(tr('join:scan.noCamera'))).toBeTruthy();
  });

  it('tells a signed-in person they already have an account', async () => {
    stubAPI({ authenticated: true, kind: 'user', csrf: 'x', me: { handle: 'alice' } }, () => undefined);
    renderJoin();
    expect(await screen.findByText(tr('join:signedIn'))).toBeTruthy();
    await waitFor(() => {
      expect(screen.queryByTestId('join-scan')).toBeNull();
    });
  });
});
