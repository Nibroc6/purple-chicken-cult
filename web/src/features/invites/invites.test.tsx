// Invites feature: view-models and the pages against a stubbed API.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/errors';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import type { DecodeReport, HandshakeAudio } from '../handshake-audio';
import { b64url, ephemeral, fromB64url, parseFrame, renderFrame } from './handshake';
import InvitePage from './InvitePage';
import InviteesPage from './InviteesPage';
import { canInvite, canShowCode, countdown, invitesErrorKey, joinURL, passed, toBudgetVM, toInviteeVM, toStatusVM } from './vm';

const qr = vi.hoisted(() => ({ toCanvas: vi.fn((_c: unknown, _p: string, _o: unknown) => Promise.resolve()) }));
vi.mock('qrcode', () => ({ default: { toCanvas: qr.toCanvas } }));

const cosign = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock('../cosign', () => ({ requestCosign: cosign.request }));

interface Call {
  method: string;
  path: string;
  body?: unknown;
}

function json(status: number, body: unknown): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function stubAPI(handler: (c: Call) => Response | undefined): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const text = await req.text();
      const c: Call = {
        method: req.method,
        path: new URL(req.url).pathname.replace(/^\/api/, ''),
        ...(text ? { body: JSON.parse(text) as unknown } : {}),
      };
      calls.push(c);
      if (c.path === '/session') return json(200, { authenticated: true, kind: 'user', csrf: 'x' });
      return handler(c) ?? json(404, { error: 'not_found' });
    }),
  );
  return calls;
}

function renderAt(path: string, el: ReactNode) {
  const router = createMemoryRouter(
    [
      { path, element: el },
      { path: '/invite/people', element: <p>people page</p> },
      { path: '/invite', element: <p>invite page</p> },
    ],
    { initialEntries: [path] },
  );
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

function person(handle: string) {
  return {
    ref: `r-${handle}`,
    handle,
    display_name: handle.toUpperCase(),
    inviter: { kind: 'user', handle: 'alice' },
    relation: 'friend',
    chain: { dist: 1, hidden_middle: 0, first_hop_plus: false },
  };
}

// openButton waits until the budget loaded and the button is enabled.
async function openButton(): Promise<HTMLElement> {
  const b = await screen.findByTestId('invite-open');
  await waitFor(() => {
    expect((b as HTMLButtonElement).disabled).toBe(false);
  });
  return b;
}

afterEach(() => {
  vi.unstubAllGlobals();
  qr.toCanvas.mockClear();
  cosign.request.mockReset();
  delete (globalThis as { pccHandshakeAudio?: unknown }).pccHandshakeAudio;
});

// status is an InvitesStatus with defaults.
function status(over: Record<string, unknown>) {
  return { kind: 'invite', mode: 'qr_audio', quiet: false, attempt: 0, attempts_left: 5, ...over };
}

const FRAME_KEY = b64url(new Uint8Array(32).fill(3));
const HS = new Uint8Array(16).fill(4);

describe('invites view-models', () => {
  it('splits countdowns into the two largest units', () => {
    const now = new Date('2026-01-01T00:00:00Z');
    expect(countdown(new Date('2026-01-04T05:06:07Z'), now)).toEqual({
      key: 'countdown.days',
      values: { d: 3, h: 5, m: 6, s: 7 },
    });
    expect(countdown(new Date('2026-01-01T02:00:30Z'), now).key).toBe('countdown.hours');
    expect(countdown(new Date('2026-01-01T00:00:00.400Z'), now)).toEqual({
      key: 'countdown.minutes',
      values: { d: 0, h: 0, m: 0, s: 1 },
    });
    expect(countdown(new Date('2025-12-31T00:00:00Z'), now).values.s).toBe(0);
  });

  it('maps budgets and knows when a code can be shown', () => {
    const b = toBudgetVM({ available: 2, cap: 3, next_at: '2026-01-02T00:00:00Z' });
    expect(b).toEqual({ available: 2, cap: 3, nextAt: new Date('2026-01-02T00:00:00Z'), revoked: false });
    expect(canInvite(b)).toBe(true);
    expect(canInvite({ ...b, available: 0 })).toBe(false);
    expect(canShowCode(b)).toBe(true);
    expect(canShowCode({ ...b, available: 0 })).toBe(true);
    expect(canShowCode({ ...b, attempts: { available: 0, cap: 20 } })).toBe(false);
    expect(canShowCode({ ...b, attempts: { available: 1, cap: 20 } })).toBe(true);
    const p = toBudgetVM({
      available: 0,
      cap: 3,
      probation: { until: '2026-01-08T00:00:00Z', need_outside_friends: 2, friend_plus: true },
    });
    expect(p.probation).toEqual({ until: new Date('2026-01-08T00:00:00Z'), needOutsideFriends: 2, friendPlus: true });
    expect(canInvite({ ...p, available: 1 })).toBe(false);
    expect(canInvite(toBudgetVM({ available: 1, cap: 3, revoked: true }))).toBe(false);
  });

  it('maps invitees, keeping the probation date only while on probation', () => {
    const on = toInviteeVM({
      person: person('bob') as never,
      joined_at: '2026-01-01T00:00:00Z',
      on_probation: true,
      probation_until: '2026-01-08T00:00:00Z',
    });
    expect(on.probationUntil).toEqual(new Date('2026-01-08T00:00:00Z'));
    const off = toInviteeVM({ person: person('bob') as never, joined_at: '2026-01-01T00:00:00Z', on_probation: false });
    expect(off.probationUntil).toBeUndefined();
  });

  it('maps handshake states and knows when an outcome passed', () => {
    const st = toStatusVM(status({ state: 'confirmed', outcome: 'retry', hint: 'volume_up', attempt: 2, attempts_left: 3 }) as never);
    expect(st).toMatchObject({ state: 'confirmed', outcome: 'retry', hint: 'volume_up', attempt: 2, attemptsLeft: 3, playing: false });
    expect(passed(st)).toBe(false);
    expect(passed({ ...st, outcome: 'accepted' })).toBe(true);
    expect(toStatusVM(status({ state: 'confirmed', sign: 'needed', peer_signed: true, hs: 'h' }) as never)).toMatchObject({
      sign: 'needed',
      peerSigned: true,
      hs: 'h',
    });
  });

  it('maps invite errors to their catalogs', () => {
    expect(invitesErrorKey(new ApiError(409, { error: 'no_invites' }))).toBe('invites:errors.no_invites');
    expect(invitesErrorKey(new ApiError(404, { error: 'not_found' }))).toBe('errors:not_found');
    expect(joinURL('https://pcc.example:8443')).toBe('pcc.example:8443/join');
  });
});

describe('invite page', () => {
  it('shows the saved invites and the countdown to the next one', async () => {
    const next = new Date(Date.now() + 2 * 3600_000 + 5 * 60_000);
    stubAPI((c) =>
      c.path === '/invites/budget' ? json(200, { available: 2, cap: 5, next_at: next.toISOString() }) : undefined,
    );
    renderAt('/invite', <InvitePage />);
    expect(await screen.findByText(tr('invites:budget.available', { count: 2, cap: 5 }))).toBeTruthy();
    expect(screen.getByTestId('invite-next').textContent).toContain(tr('invites:countdown.hours', { h: 2, m: 5 }).slice(0, 7));
    expect(screen.getByTestId<HTMLButtonElement>('invite-open').disabled).toBe(false);
  });

  it('labels the code attempts and offers no code once they are used up', async () => {
    const next = new Date(Date.now() + 40 * 60_000);
    stubAPI((c) =>
      c.path === '/invites/budget'
        ? json(200, { available: 2, cap: 5, attempts: { available: 0, cap: 20, next_at: next.toISOString() } })
        : undefined,
    );
    renderAt('/invite', <InvitePage />);
    expect(await screen.findByText(tr('invites:attempts.left', { count: 0, cap: 20 }))).toBeTruthy();
    expect(screen.getByTestId('invite-attempts-next')).toBeTruthy();
    expect(screen.getByText(tr('invites:attempts.hint'))).toBeTruthy();
    expect(screen.getByTestId<HTMLButtonElement>('invite-open').disabled).toBe(true);
  });

  it('shows what is left of probation and offers a connect-only code', async () => {
    const until = new Date(Date.now() + 4 * 86400_000 + 3600_000 + 30_000);
    stubAPI((c) =>
      c.path === '/invites/budget'
        ? json(200, {
            available: 0,
            cap: 3,
            probation: { until: until.toISOString(), need_outside_friends: 1, friend_plus: true },
          })
        : undefined,
    );
    renderAt('/invite', <InvitePage />);
    expect(await screen.findByText(tr('invites:budget.probationFriendsPlus', { count: 1 }))).toBeTruthy();
    expect(screen.getByTestId('invite-probation-time').textContent).toContain(
      tr('invites:countdown.days', { d: 4, h: 1 }),
    );
    // The code still opens: on probation it only connects people signed in.
    expect(screen.getByTestId<HTMLButtonElement>('invite-open').disabled).toBe(false);
    expect(screen.getByText(tr('invites:open.connectOnly'))).toBeTruthy();
  });

  it('opens a handshake, renders frames from the key, confirms, listens, reports and co-signs', async () => {
    let state = 'open';
    let st: Record<string, unknown> = status({ state });
    const peer = await ephemeral();
    let serverFrame = '';
    const heard: DecodeReport = { ultraBits: Uint8Array.of(1, 2, 3, 4), jingleBits: Uint8Array.of(5, 6, 7), tUltraMs: 812.4, motifLevelDb: -18 };
    const listen = vi.fn(() => new Promise<DecodeReport>((r) => setTimeout(() => {
      r(heard);
    }, 50)));
    (globalThis as { pccHandshakeAudio?: () => HandshakeAudio }).pccHandshakeAudio = () => ({
      play: () => Promise.resolve({ startedAt: 0 }),
      listen,
    });
    cosign.request.mockResolvedValue({ intent: 'intent-1', assertion: { id: 'a' } });
    const calls = stubAPI((c) => {
      if (c.path === '/invites/budget') return json(200, { available: 1, cap: 3, next_at: new Date(Date.now() + 1e7).toISOString() });
      if (c.path === '/invites/handshakes' && c.method === 'POST') {
        return json(201, { hs: 'h1', mode: 'qr_audio', frame_ttl_ms: 250, frame_key: FRAME_KEY });
      }
      if (c.path === '/invites/handshakes/h1/epub') return json(204, null);
      if (c.path === '/invites/handshakes/h1/frame') {
        return json(200, { payload: serverFrame, valid_until: new Date(Date.now() + 250).toISOString() });
      }
      if (c.path === '/invites/handshakes/h1') return json(200, st);
      if (c.path === '/invites/handshakes/h1/peer') return json(200, { e_v: b64url(peer.pub) });
      if (c.path === '/invites/handshakes/h1/confirm') {
        state = 'confirmed';
        st = status({ state, playing: true, attempt: 1, attempts_left: 4 });
        return json(204, null);
      }
      if (c.path === '/invites/handshakes/h1/report') {
        st = status({ state, attempt: 1, attempts_left: 4, outcome: 'verified', sign: 'needed' });
        return json(200, { outcome: 'verified' });
      }
      if (c.path === '/invites/handshakes/h1/cosign') {
        st = status({ state: 'done', attempt: 1, attempts_left: 4, outcome: 'verified' });
        return json(204, null);
      }
      return undefined;
    });
    // The server's frame needs the E_i the device posts: render it when asked.
    const origFetch = globalThis.fetch;
    vi.stubGlobal('fetch', async (req: Request) => {
      if (new URL(req.url).pathname === '/api/invites/handshakes/h1/epub') {
        const { e_i } = (await req.clone().json()) as { e_i: string };
        serverFrame = await renderFrame(new Uint8Array(32).fill(3), { hs: HS, ei: fromB64url(e_i), n: 12 });
      }
      return origFetch(req);
    });
    renderAt('/invite', <InvitePage />);
    fireEvent.click(await openButton());
    await waitFor(() => {
      expect(qr.toCanvas).toHaveBeenCalled();
    });
    const drawn = qr.toCanvas.mock.calls.map((a) => a[1]);
    const f = parseFrame(drawn[0] ?? '');
    expect(f?.n).toBeGreaterThanOrEqual(12);
    expect(b64url(f?.hs ?? new Uint8Array())).toBe(b64url(HS));
    const epub = calls.find((c) => c.path === '/invites/handshakes/h1/epub');
    expect(b64url(f?.ei ?? new Uint8Array())).toBe((epub?.body as { e_i: string }).e_i);
    expect(screen.getByRole('img', { name: tr('invites:hs.qrLabel') })).toBeTruthy();

    state = 'claimed';
    st = status({ state, sas: '042137' });
    expect((await screen.findByTestId('invite-sas', {}, { timeout: 3000 })).textContent).toBe('042137');
    fireEvent.click(screen.getByTestId('invite-match'));
    expect(await screen.findByTestId('invite-done', {}, { timeout: 5000 })).toBeTruthy();
    expect(listen).toHaveBeenCalled();
    const rep = calls.find((c) => c.path === '/invites/handshakes/h1/report')?.body as Record<string, unknown>;
    expect(rep).toMatchObject({ ultra_bits: b64url(heard.ultraBits ?? new Uint8Array()), jingle_bits: 'BQYH', t_ultra_ms: 812 });
    expect(typeof rep.kmac_commit).toBe('string');
    expect(cosign.request).toHaveBeenCalledWith(expect.objectContaining({ purpose: 'invite_inviter', target: 'h1' }));
    expect(calls.find((c) => c.path === '/invites/handshakes/h1/cosign')?.body).toEqual({ intent: 'intent-1', assertion: { id: 'a' } });
  });

  it('shows the hint of a failed attempt and opens dual-QR handshakes', async () => {
    let st: Record<string, unknown> = status({ state: 'confirmed', mode: 'dual_qr', outcome: 'retry', attempt: 1, attempts_left: 4 });
    const peer = await ephemeral();
    const calls = stubAPI((c) => {
      if (c.path === '/invites/budget') return json(200, { available: 1, cap: 3 });
      if (c.path === '/invites/handshakes' && c.method === 'POST') {
        return json(201, { hs: 'h2', mode: 'dual_qr', frame_ttl_ms: 250, frame_key: FRAME_KEY });
      }
      if (c.path === '/invites/handshakes/h2/epub') return json(204, null);
      if (c.path === '/invites/handshakes/h2/frame') {
        return json(200, { payload: `pcc2.${b64url(HS)}.${b64url(peer.pub)}.0.AAAAAAAAAAAAAA`, valid_until: new Date().toISOString() });
      }
      if (c.path === '/invites/handshakes/h2/peer') return json(200, { e_v: b64url(peer.pub) });
      if (c.path === '/invites/handshakes/h2') return json(200, st);
      return undefined;
    });
    renderAt('/invite', <InvitePage />);
    fireEvent.click(screen.getByTestId('invite-mode-dual'));
    fireEvent.click(await openButton());
    expect((await screen.findByTestId('invite-outcome')).dataset.outcome).toBe('retry');
    await screen.findByTestId('invite-dual-qr');
    await waitFor(() => {
      expect(qr.toCanvas.mock.calls.some((a) => a[1].startsWith('pcc2d.'))).toBe(true);
    });
    expect(calls.find((c) => c.path === '/invites/handshakes' && c.method === 'POST')?.body).toEqual({ mode: 'dual_qr', quiet: false });
    st = status({ state: 'confirmed', outcome: 'retry', hint: 'volume_up', attempt: 2, attempts_left: 3 });
    expect(await screen.findByText(tr('invites:outcome.retryVolume'), {}, { timeout: 3000 })).toBeTruthy();
  });

  it('cancels when the codes do not match and offers a new code', async () => {
    let state = 'claimed';
    stubAPI((c) => {
      if (c.path === '/invites/budget') return json(200, { available: 1, cap: 3 });
      if (c.path === '/invites/handshakes' && c.method === 'POST') {
        return json(201, { hs: 'h1', mode: 'qr_audio', frame_ttl_ms: 250, frame_key: FRAME_KEY });
      }
      if (c.path === '/invites/handshakes/h1/epub') return json(204, null);
      if (c.path === '/invites/handshakes/h1/frame') return json(404, { error: 'not_found' });
      if (c.path === '/invites/handshakes/h1' && c.method === 'DELETE') {
        state = 'cancelled';
        return json(204, null);
      }
      if (c.path === '/invites/handshakes/h1') return json(200, status(state === 'claimed' ? { state, sas: '111111' } : { state }));
      return undefined;
    });
    renderAt('/invite', <InvitePage />);
    fireEvent.click(await openButton());
    fireEvent.click(await screen.findByTestId('invite-mismatch'));
    expect(await screen.findByText(tr('invites:hs.cancelled'))).toBeTruthy();
    expect(screen.getByTestId('invite-again')).toBeTruthy();
  });

  it('shows why a code cannot be opened', async () => {
    stubAPI((c) => {
      if (c.path === '/invites/budget') return json(200, { available: 1, cap: 3 });
      if (c.path === '/invites/handshakes') return json(409, { error: 'friend_cap' });
      return undefined;
    });
    renderAt('/invite', <InvitePage />);
    fireEvent.click(await openButton());
    expect(await screen.findByText(tr('invites:errors.friend_cap'))).toBeTruthy();
  });
});

describe('invitees page', () => {
  it('lists invitees with their probation state', async () => {
    stubAPI((c) =>
      c.path === '/invites/invitees'
        ? json(200, {
            items: [
              { person: person('bob'), joined_at: '2026-01-01T00:00:00Z', on_probation: true, probation_until: '2026-01-08T00:00:00Z' },
              { person: person('carol'), joined_at: '2025-06-01T00:00:00Z', on_probation: false },
            ],
          })
        : undefined,
    );
    renderAt('/invite/people', <InviteesPage />);
    expect(await screen.findAllByTestId('person-card')).toHaveLength(2);
    expect(screen.getAllByTestId('invitee-probation')).toHaveLength(1);
  });

  it('says when nobody was invited yet', async () => {
    stubAPI((c) => (c.path === '/invites/invitees' ? json(200, { items: [] }) : undefined));
    renderAt('/invite/people', <InviteesPage />);
    expect(await screen.findByText(tr('invites:people.empty'))).toBeTruthy();
  });
});
