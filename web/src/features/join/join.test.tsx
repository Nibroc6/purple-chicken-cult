// Join feature: QR decoding, view-models and the join flow against a stubbed
// API, a stubbed camera scanner and a stubbed passkey prompt.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import QRCode from 'qrcode';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/errors';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import { PasskeyError } from '../auth/webauthn';
import type { HandshakeAudio, PlayBits } from '../handshake-audio';
import { ephemeral, fromB64url, renderFrame } from '../invites/handshake';
import { decodeFrame, isInviteFrame } from './decode';
import JoinPage from './JoinPage';
import { browserLanguages, joinErrorKey } from './vm';

const FRAME_KEY = new Uint8Array(32).fill(3);
const HS = new Uint8Array(16).fill(4);
let FRAME = '';
let REFUSED = '';
const DUAL = 'pcc2d.BAQEBAQEBAQEBAQEBAQEBA.9.BBBBBBBBBBBBBB';

beforeAll(async () => {
  const ei = (await ephemeral()).pub;
  FRAME = await renderFrame(FRAME_KEY, { hs: HS, ei, n: 5 });
  REFUSED = await renderFrame(FRAME_KEY, { hs: HS, ei, n: 1 });
});

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

const pk = vi.hoisted(() => ({ create: vi.fn(), get: vi.fn() }));
vi.mock('../auth/webauthn', async (orig) => ({
  ...(await orig<typeof import('../auth/webauthn')>()),
  createPasskey: pk.create,
  getAssertion: pk.get,
}));

const cosign = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock('../cosign', async (orig) => ({
  ...(await orig<typeof import('../cosign')>()),
  requestCosign: cosign.request,
}));

interface Call {
  method: string;
  path: string;
  body: unknown;
  sig: string | null;
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
        sig: req.headers.get('X-PCC-Join-Sig'),
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
  pk.get.mockReset();
  cosign.request.mockReset();
  delete (globalThis as { pccHandshakeAudio?: unknown }).pccHandshakeAudio;
});

function status(over: Record<string, unknown>) {
  return { kind: 'invite', mode: 'qr_audio', quiet: false, attempt: 0, attempts_left: 5, ...over };
}

// fakeAudio records what the page plays.
function fakeAudio(): PlayBits[] {
  const played: PlayBits[] = [];
  (globalThis as { pccHandshakeAudio?: () => HandshakeAudio }).pccHandshakeAudio = () => ({
    play: (bits: PlayBits) => {
      played.push(bits);
      return Promise.resolve({ startedAt: 0 });
    },
    listen: () => Promise.reject(new Error('not here')),
  });
  return played;
}

describe('QR decoding', () => {
  it('decodes an invite frame and ignores other codes', () => {
    const img = qrImage(FRAME);
    expect(decodeFrame(img.data, img.size, img.size)).toBe(FRAME);
    const other = qrImage('https://example.com/');
    expect(decodeFrame(other.data, other.size, other.size)).toBeNull();
    const blank = new Uint8ClampedArray(64 * 64 * 4).fill(255);
    expect(decodeFrame(blank, 64, 64)).toBeNull();
  });

  it('recognizes v2 frames and dual-QR frames only', () => {
    expect(isInviteFrame(FRAME)).toBe(true);
    expect(isInviteFrame(DUAL)).toBe(true);
    expect(isInviteFrame(`${FRAME}x`)).toBe(false);
    expect(isInviteFrame('pcc1.AAAAAAAAAAAAAAAAAAAAAA.177724320.BBBBBBBBBBBBBB')).toBe(false);
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

  it('scans, shows the SAS, plays the sound, registers and signs the drafts', async () => {
    let st: Record<string, unknown> = status({ state: 'claimed', sas: '424242' });
    cam.frames = ['pcc2.not-a-frame', REFUSED, REFUSED, FRAME];
    const played = fakeAudio();
    pk.create.mockResolvedValue({ id: 'cred' });
    pk.get.mockResolvedValue({ id: 'cred', response: { signature: 'sig' } });
    let cosigns = 0;
    const calls = stubAPI({ authenticated: false, kind: 'none' }, (c) => {
      if (c.path === '/join/start') return json(200, start);
      if (c.path === '/join/claim') {
        return (c.body as { payload: string }).payload === FRAME ? json(204, null) : json(404, { error: 'not_found' });
      }
      if (c.path === '/join/status') return json(200, st);
      if (c.path === '/join/audio/start') {
        st = status({ state: 'confirmed', attempt: 1, attempts_left: 4, outcome: 'verified', signup_token: 'tok' });
        return json(200, { play_at: new Date().toISOString(), attempt: 1 });
      }
      if (c.path === '/join/register/begin') {
        const b = c.body as { handle: string };
        return b.handle === 'taken'
          ? json(409, { error: 'handle_taken', field: 'handle' })
          : json(200, { options: { publicKey: { challenge: 'x' } } });
      }
      if (c.path === '/join/register/finish') {
        return json(200, {
          options: { publicKey: { challenge: 'y' } },
          records: [
            { kind: 'invite', schema_version: 1, inviter: 'i', invitee: 'v', cosign: { nonce: 'n', issued_at: '2026-01-01T00:00:00Z' } },
            { kind: 'friendship', schema_version: 1, a: 'i', b: 'v', level: 'friend+' },
          ],
        });
      }
      if (c.path === '/join/register/cosign') {
        cosigns++;
        if (cosigns === 1) {
          st = { ...st, peer_signed: true };
          return json(409, { error: 'awaiting_inviter' });
        }
        return json(201, {
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
    st = status({ state: 'confirmed' });

    fireEvent.click(await screen.findByTestId('join-play', {}, { timeout: 3000 }));
    const submit = await screen.findByTestId('join-submit', {}, { timeout: 3000 });
    expect(played).toHaveLength(1);
    expect(played[0]?.ultra).toHaveLength(4);
    expect(played[0]?.jingle).toHaveLength(3);
    expect((submit as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText(tr('join:form.handle')), { target: { value: 'taken' } });
    fireEvent.click(screen.getByLabelText(tr('join:form.tos', { version: 3 })));
    fireEvent.click(screen.getByLabelText(tr('join:form.age', { age: 16 })));
    fireEvent.click(submit);
    expect(await screen.findByText(tr('join:errors.handle_taken'))).toBeTruthy();

    fireEvent.change(screen.getByLabelText(tr('join:form.handle')), { target: { value: ' newbie ' } });
    fireEvent.click(screen.getByTestId('join-submit'));
    // The drafts are shown before the new passkey signs them.
    expect(await screen.findAllByTestId('cosign-record')).toHaveLength(2);
    fireEvent.click(screen.getByTestId('join-sign'));
    expect((await screen.findByTestId('join-invited-by', {}, { timeout: 5000 })).textContent).toBe(
      tr('join:done.invitedBy', { handle: '@alice' }),
    );
    expect(screen.getByRole('heading', { name: tr('auth:second.title') })).toBeTruthy();

    const begin = calls.filter((c) => c.path === '/join/register/begin').map((c) => c.body);
    expect(begin[1]).toMatchObject({ handle: 'newbie', tos_version: 3, age_attested: true, signup_token: 'tok' });
    const claims = calls.filter((c) => c.path === '/join/claim').map((c) => c.body as { payload: string; e_v: string; proof: string });
    // Not a frame: never sent; refused: sent once; then the fresh one.
    expect(claims.map((c) => c.payload)).toEqual([REFUSED, FRAME]);
    expect(fromB64url(claims[0]?.e_v ?? '')).toHaveLength(65);
    expect(fromB64url(claims[0]?.proof ?? '')).toHaveLength(64);
    expect(calls.find((c) => c.path === '/join/register/finish')?.body).toEqual({ credential: { id: 'cred' } });
    // Every later join call is signed; the same assertion goes again once the inviter signed.
    expect(calls.filter((c) => c.path !== '/join/start' && c.path !== '/join/claim' && c.path !== '/session').every((c) => c.sig)).toBe(true);
    const cos = calls.filter((c) => c.path === '/join/register/cosign').map((c) => c.body);
    expect(cos).toHaveLength(2);
    expect(cos[0]).toEqual(cos[1]);
    expect(pk.get).toHaveBeenCalledTimes(1);
  });

  it('shows the hint of a failed attempt and plays again', async () => {
    let st: Record<string, unknown> = status({ state: 'confirmed', quiet: true });
    cam.frames = [FRAME];
    const played = fakeAudio();
    let n = 0;
    stubAPI({ authenticated: false, kind: 'none' }, (c) => {
      if (c.path === '/join/start') return json(200, start);
      if (c.path === '/join/claim') return json(204, null);
      if (c.path === '/join/status') return json(200, st);
      if (c.path === '/join/audio/start') {
        n++;
        st = status({ state: 'confirmed', quiet: true, attempt: n, attempts_left: 5 - n, outcome: 'retry', hint: 'try_jingle' });
        return json(200, { play_at: new Date().toISOString(), attempt: n });
      }
      return undefined;
    });
    renderJoin();
    fireEvent.click(await screen.findByTestId('join-scan'));
    fireEvent.click(await screen.findByTestId('join-play', {}, { timeout: 3000 }));
    expect(await screen.findByText(tr('join:sound.retryJingle'), {}, { timeout: 3000 })).toBeTruthy();
    fireEvent.click(screen.getByTestId('join-play'));
    await waitFor(() => {
      expect(played).toHaveLength(2);
    });
  });

  it('lets a signed-in friend scan an upgrade code and co-sign friend+', async () => {
    let st: Record<string, unknown> = status({ kind: 'upgrade', state: 'confirmed' });
    cam.frames = [FRAME];
    fakeAudio();
    cosign.request.mockResolvedValue({ intent: 'i-1', assertion: { id: 'a' } });
    const calls = stubAPI({ authenticated: true, kind: 'user', csrf: 'x', me: { handle: 'bob' } }, (c) => {
      if (c.path === '/join/claim') return json(204, null);
      if (c.path === '/join/status') return json(200, st);
      if (c.path === '/join/audio/start') {
        st = status({ kind: 'upgrade', state: 'confirmed', attempt: 1, attempts_left: 4, outcome: 'verified', sign: 'needed', hs: 'mine' });
        return json(200, { play_at: new Date().toISOString(), attempt: 1 });
      }
      if (c.path === '/invites/handshakes/mine/cosign') {
        st = status({ kind: 'upgrade', state: 'done', attempt: 1, attempts_left: 4, outcome: 'verified' });
        return json(204, null);
      }
      return undefined;
    });
    renderJoin();
    expect(await screen.findByText(tr('join:signedIn'))).toBeTruthy();
    fireEvent.click(screen.getByTestId('join-scan-friend'));
    fireEvent.click(await screen.findByTestId('join-play', {}, { timeout: 3000 }));
    expect(await screen.findByTestId('join-friend-plus', {}, { timeout: 5000 })).toBeTruthy();
    expect(cosign.request).toHaveBeenCalledWith(expect.objectContaining({ purpose: 'friend_upgrade', target: 'mine' }));
    expect(calls.some((c) => c.path === '/join/start')).toBe(false);
    expect(calls.find((c) => c.path === '/invites/handshakes/mine/cosign')?.body).toEqual({ intent: 'i-1', assertion: { id: 'a' } });
  });

  it('scans the second code in dual-QR mode', async () => {
    let st: Record<string, unknown> = status({ state: 'confirmed', mode: 'dual_qr' });
    cam.frames = [FRAME, DUAL];
    const calls = stubAPI({ authenticated: false, kind: 'none' }, (c) => {
      if (c.path === '/join/start') return json(200, start);
      if (c.path === '/join/claim') return json(204, null);
      if (c.path === '/join/status') return json(200, st);
      if (c.path === '/join/dual/frame') {
        st = status({ state: 'confirmed', mode: 'dual_qr', attempt: 1, attempts_left: 4, outcome: 'accepted', signup_token: 't' });
        return json(200, { outcome: 'accepted' });
      }
      return undefined;
    });
    renderJoin();
    fireEvent.click(await screen.findByTestId('join-scan'));
    expect(await screen.findByTestId('join-submit', {}, { timeout: 5000 })).toBeTruthy();
    expect((calls.find((c) => c.path === '/join/dual/frame')?.body as { payload: string }).payload).toBe(DUAL);
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
    expect(screen.getByTestId('join-scan-friend')).toBeTruthy();
  });
});
