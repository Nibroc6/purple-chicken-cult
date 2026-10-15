// Invites feature: view-models and the pages against a stubbed API.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/errors';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import InvitePage from './InvitePage';
import InviteesPage from './InviteesPage';
import { canOpen, countdown, invitesErrorKey, joinURL, toBudgetVM, toInviteeVM } from './vm';

const qr = vi.hoisted(() => ({ toCanvas: vi.fn(() => Promise.resolve()) }));
vi.mock('qrcode', () => ({ default: { toCanvas: qr.toCanvas } }));

interface Call {
  method: string;
  path: string;
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
    vi.fn((req: Request) => {
      const c = { method: req.method, path: new URL(req.url).pathname.replace(/^\/api/, '') };
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
});

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
    expect(canOpen(b)).toBe(true);
    expect(canOpen({ ...b, available: 0 })).toBe(false);
    const p = toBudgetVM({ available: 0, cap: 3, probation: { until: '2026-01-08T00:00:00Z', need_outside_friends: 2 } });
    expect(p.probation).toEqual({ until: new Date('2026-01-08T00:00:00Z'), needOutsideFriends: 2 });
    expect(canOpen({ ...p, available: 1 })).toBe(false);
    expect(canOpen(toBudgetVM({ available: 1, cap: 3, revoked: true }))).toBe(false);
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

  it('shows what is left of probation and offers no code', async () => {
    const until = new Date(Date.now() + 4 * 86400_000 + 3600_000 + 30_000);
    stubAPI((c) =>
      c.path === '/invites/budget'
        ? json(200, { available: 0, cap: 3, probation: { until: until.toISOString(), need_outside_friends: 1 } })
        : undefined,
    );
    renderAt('/invite', <InvitePage />);
    expect(await screen.findByText(tr('invites:budget.probationFriends', { count: 1 }))).toBeTruthy();
    expect(screen.getByTestId('invite-probation-time').textContent).toContain(
      tr('invites:countdown.days', { d: 4, h: 1 }),
    );
    expect(screen.getByTestId<HTMLButtonElement>('invite-open').disabled).toBe(true);
  });

  it('opens a handshake, draws the QR, shows the SAS and confirms', async () => {
    let state = 'open';
    const calls = stubAPI((c) => {
      if (c.path === '/invites/budget') return json(200, { available: 1, cap: 3, next_at: new Date(Date.now() + 1e7).toISOString() });
      if (c.path === '/invites/handshakes' && c.method === 'POST') return json(201, { hs: 'h1', mode: 'qr', frame_ttl_ms: 10000 });
      if (c.path === '/invites/handshakes/h1/frame') {
        return json(200, { payload: 'pcc1.frame', valid_until: new Date(Date.now() + 10_000).toISOString() });
      }
      if (c.path === '/invites/handshakes/h1') return json(200, state === 'open' ? { state } : { state, sas: '042137' });
      if (c.path === '/invites/handshakes/h1/confirm') {
        state = 'confirmed';
        return json(204, null);
      }
      return undefined;
    });
    renderAt('/invite', <InvitePage />);
    fireEvent.click(await openButton());
    await waitFor(() => {
      expect(qr.toCanvas).toHaveBeenCalledWith(expect.anything(), 'pcc1.frame', expect.anything());
    });
    expect(screen.getByRole('img', { name: tr('invites:hs.qrLabel') })).toBeTruthy();
    state = 'claimed';
    expect((await screen.findByTestId('invite-sas', {}, { timeout: 3000 })).textContent).toBe('042137');
    fireEvent.click(screen.getByTestId('invite-match'));
    expect(await screen.findByText(tr('invites:hs.confirmed'))).toBeTruthy();
    expect(calls.filter((c) => c.path === '/invites/handshakes/h1/confirm' && c.method === 'POST')).toHaveLength(1);
  });

  it('cancels when the codes do not match and offers a new code', async () => {
    let state = 'claimed';
    stubAPI((c) => {
      if (c.path === '/invites/budget') return json(200, { available: 1, cap: 3 });
      if (c.path === '/invites/handshakes' && c.method === 'POST') return json(201, { hs: 'h1', mode: 'qr', frame_ttl_ms: 10000 });
      if (c.path === '/invites/handshakes/h1' && c.method === 'DELETE') {
        state = 'cancelled';
        return json(204, null);
      }
      if (c.path === '/invites/handshakes/h1') return json(200, state === 'claimed' ? { state, sas: '111111' } : { state });
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
