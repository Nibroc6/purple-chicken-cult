// Guardian recovery pages against a stubbed API.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/errors';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import { recoveryErrorKey } from './api';
import RecoverPage from './RecoverPage';
import RecoverySettingsPage from './RecoverySettingsPage';
import { clearTicket, loadTicket, pollEvery, saveTicket, toStatusVM } from './vm';

vi.mock('../cosign', async (orig) => ({
  ...(await orig<typeof import('../cosign')>()),
  requestCosign: vi.fn(() => Promise.resolve({ intent: 'intent1', assertion: { id: 'cred' } })),
}));

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

interface Seen {
  method: string;
  path: string;
  body: unknown;
  headers: Headers;
}

const me = {
  ref: 'me',
  handle: 'gina',
  display_name: 'Gina',
  role: 'member',
  inviter: { kind: 'genesis' },
  settings: { ui_locale: 'en', languages: ['en'], autotranslate: false, rank_preset: 'balanced', ui_prefs: {} },
};

function stub(handler: (method: string, path: string, body: unknown) => Response | undefined, signedIn = true) {
  const seen: Seen[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname.replace(/^\/api/, '');
      const text = await req.text();
      const body = text ? (JSON.parse(text) as unknown) : undefined;
      seen.push({ method: req.method, path, body, headers: req.headers });
      if (path === '/session') {
        return json(200, signedIn ? { authenticated: true, kind: 'user', csrf: 'x', me } : { authenticated: false, kind: 'none' });
      }
      if (path === '/me' && signedIn) return json(200, me);
      return handler(req.method, path, body) ?? json(404, { error: 'not_found' });
    }),
  );
  return seen;
}

function renderAt(path: string, element: React.ReactNode) {
  const router = createMemoryRouter([{ path, element }], { initialEntries: [path] });
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  clearTicket();
});

afterEach(() => {
  vi.unstubAllGlobals();
  clearTicket();
});

describe('recovery view-models', () => {
  it('polls while the guardians act, rarely during the hold, never after', () => {
    expect(pollEvery(undefined)).toBe(5_000);
    expect(pollEvery('claimed')).toBe(5_000);
    expect(pollEvery('approving')).toBe(5_000);
    expect(pollEvery('holding')).toBe(60_000);
    expect(pollEvery('released')).toBe(false);
    expect(pollEvery('cancelled')).toBe(false);
    expect(pollEvery('expired')).toBe(false);
  });

  it('keeps the ticket in this browser', () => {
    expect(loadTicket()).toBeUndefined();
    saveTicket('tk');
    expect(loadTicket()).toBe('tk');
    clearTicket();
    expect(loadTicket()).toBeUndefined();
  });

  it('maps the status and the problems', () => {
    expect(toStatusVM({ state: 'holding', release_at: '2026-01-02T03:04:05Z' })).toEqual({
      state: 'holding',
      releaseAt: new Date('2026-01-02T03:04:05Z'),
    });
    expect(recoveryErrorKey(new ApiError(409, { error: 'second_guardian_required' }))).toBe(
      'recovery:errors.second_guardian_required',
    );
    expect(recoveryErrorKey(new ApiError(404, { error: 'not_found' }))).toBe('errors:not_found');
  });
});

describe('the recovering device', () => {
  it('starts with the steps when it has no ticket', async () => {
    stub(() => undefined, false);
    renderAt('/recover', <RecoverPage />);
    expect(await screen.findByTestId('recover-scan')).toBeTruthy();
  });

  it('follows its ticket to the enrollment link', async () => {
    saveTicket('ticket1');
    let state = 'holding';
    const seen = stub((method, path) => {
      if (method === 'GET' && path === '/recovery/status') {
        return json(
          200,
          state === 'released'
            ? { state, release_at: '2026-01-02T03:04:05Z', enroll_url: 'https://pcc.example/enroll#tok' }
            : { state, release_at: '2026-01-02T03:04:05Z' },
        );
      }
      return undefined;
    }, false);
    renderAt('/recover', <RecoverPage />);
    await waitFor(() => {
      expect(screen.getByTestId('recover-state').getAttribute('data-state')).toBe('holding');
    });
    const ask = seen.find((s) => s.path === '/recovery/status');
    expect(ask?.headers.get('X-PCC-Recovery')).toBe('ticket1');
    state = 'released';
    fireEvent.focus(window);
    window.dispatchEvent(new Event('visibilitychange'));
    const link = await screen.findByTestId('recover-enroll', {}, { timeout: 3000 });
    expect(link.getAttribute('href')).toBe('https://pcc.example/enroll#tok');
    fireEvent.click(link);
    expect(loadTicket()).toBeUndefined();
  });

  it('forgets a ticket the server does not know', async () => {
    saveTicket('gone');
    stub(() => undefined, false);
    renderAt('/recover', <RecoverPage />);
    expect(await screen.findByText(i18n.t('recovery:recover.unknown'))).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: i18n.t('recovery:recover.again') }));
    expect(await screen.findByTestId('recover-scan')).toBeTruthy();
    expect(loadTicket()).toBeUndefined();
  });
});

describe('the guardians', () => {
  it('lists friend+ friends only and starts a recovery in person', async () => {
    const seen = stub((method, path) => {
      if (path === '/recovery/requests') return json(200, { items: [] });
      if (path === '/friends') {
        return json(200, {
          items: [
            { ref: 'fp', handle: 'alice', display_name: 'Alice', relation: 'friend_plus', inviter: { kind: 'genesis' }, chain: { kind: 'direct' } },
            { ref: 'pf', handle: 'pat', display_name: '', relation: 'friend', inviter: { kind: 'genesis' }, chain: { kind: 'direct' } },
          ],
        });
      }
      if (method === 'POST' && path === '/recovery/sessions') {
        return json(201, { hs: 'hs1', mode: 'qr_audio', frame_ttl_ms: 250, frame_key: 'AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE' });
      }
      return undefined;
    });
    renderAt('/settings/recovery', <RecoverySettingsPage />);
    expect(await screen.findByText(i18n.t('recovery:approve.none'))).toBeTruthy();
    const friends = await screen.findAllByTestId('recovery-friend');
    expect(friends.map((f) => f.getAttribute('data-handle'))).toEqual(['alice']);
    fireEvent.click(screen.getByTestId('recovery-start'));
    expect(await screen.findByTestId('invite-handshake')).toBeTruthy();
    expect(seen.find((s) => s.method === 'POST' && s.path === '/recovery/sessions')?.body).toEqual({ friend: 'fp' });
  });

  it('approves as the second guardian with a co-signature', async () => {
    let approved = false;
    const seen = stub((method, path) => {
      if (path === '/recovery/requests') {
        return json(200, {
          items: approved ? [] : [{ id: 'req1', account: { handle: 'alice', display_name: 'Alice' }, expires_at: '2026-01-02T03:04:05Z' }],
        });
      }
      if (path === '/friends') return json(200, { items: [] });
      if (path === '/auth/passkeys') return json(200, { items: [{ ref: 'k', name: 'key', current: true, created_at: '2026-01-01T00:00:00Z' }] });
      if (method === 'POST' && path === '/recovery/requests/req1/approve') {
        approved = true;
        return new Response(null, { status: 204 });
      }
      return undefined;
    });
    renderAt('/settings/recovery', <RecoverySettingsPage />);
    const item = await screen.findByTestId('recovery-request');
    expect(item.getAttribute('data-handle')).toBe('alice');
    fireEvent.click(screen.getByTestId('recovery-approve'));
    await waitFor(() => {
      expect(seen.some((s) => s.path === '/recovery/requests/req1/approve')).toBe(true);
    });
    expect(seen.find((s) => s.path === '/recovery/requests/req1/approve')?.body).toEqual({
      intent: 'intent1',
      assertion: { id: 'cred' },
    });
  });
});
