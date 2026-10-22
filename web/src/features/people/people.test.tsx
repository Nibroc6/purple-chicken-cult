// People feature: view-models and the pages against a stubbed API.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/errors';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import { asRef } from '../../vm/types';
import BlocksPage from './BlocksPage';
import FriendsPage from './FriendsPage';
import PersonPage from './PersonPage';
import RequestsPage from './RequestsPage';
import SuggestionsPage from './SuggestionsPage';
import { ReportModal } from '../moderation/ReportModal';
import { canUpgrade, cosignPurpose, peopleErrorKey, primaryActions, secondaryActions, toPersonVM } from './vm';

// The co-sign dialog is the cosign feature's; here it signs (or is cancelled) at once.
interface Signed {
  intent: string;
  assertion: Record<string, unknown>;
}
const cosign = vi.hoisted(() => ({ result: null as Signed | null, asked: [] as unknown[] }));
vi.mock('../cosign', () => ({
  requestCosign: (req: unknown) => {
    cosign.asked.push(req);
    return Promise.resolve(cosign.result);
  },
}));

interface Call {
  method: string;
  path: string;
  body?: string;
}

function json(status: number, body: unknown): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function person(handle: string, over: Record<string, unknown> = {}) {
  return {
    ref: `r-${handle}`,
    handle,
    display_name: handle.toUpperCase(),
    inviter: { kind: 'user', handle: 'carol' },
    relation: 'connected',
    chain: { dist: 2, first_hop: { ref: 'r-carol', handle: 'carol' }, hidden_middle: 0, first_hop_plus: false },
    ...over,
  };
}

// stubAPI routes every request through handler and records the calls.
function stubAPI(handler: (c: Call) => Response | undefined): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const text = await req.clone().text();
      const c: Call = { method: req.method, path: new URL(req.url).pathname.replace(/^\/api/, ''), ...(text ? { body: text } : {}) };
      calls.push(c);
      if (c.path === '/session') return json(200, { authenticated: true, kind: 'user', csrf: 'x' });
      return handler(c) ?? json(404, { error: 'not_found' });
    }),
  );
  return calls;
}

function renderAt(path: string, pattern: string, el: ReactNode) {
  const router = createMemoryRouter(
    [
      {
        path: pattern,
        element: (
          <>
            {el}
            <ReportModal />
          </>
        ),
      },
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

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('people view-models', () => {
  it('maps a PersonView', () => {
    const p = toPersonVM(person('bob', { request: 'outgoing', muted: true }) as never);
    expect(p).toEqual({
      ref: asRef('r-bob'),
      handle: 'bob',
      displayName: 'BOB',
      inviter: { kind: 'user', handle: 'carol' },
      relation: 'connected',
      request: 'outgoing',
      chain: { dist: 2, firstHop: { ref: asRef('r-carol'), handle: 'carol' }, middle: [], hiddenMiddle: 0, firstHopPlus: false },
      blocked: false,
      muted: true,
      hiddenVia: false,
    });
    expect(toPersonVM(person('bob', { hidden_via: true }) as never).hiddenVia).toBe(true);
  });

  it('offers actions by relation and request state', () => {
    const vm = (over: Record<string, unknown>) => toPersonVM(person('bob', over) as never);
    expect(primaryActions(vm({}))).toEqual(['request']);
    expect(primaryActions(vm({ request: 'outgoing' }))).toEqual(['withdraw']);
    expect(primaryActions(vm({ request: 'incoming' }))).toEqual(['accept', 'decline']);
    expect(primaryActions(vm({ relation: 'friend' }))).toEqual(['unfriend']);
    expect(primaryActions(vm({ relation: 'self' }))).toEqual([]);
    expect(primaryActions(vm({ blocked: true }))).toEqual(['unblock']);
    expect(secondaryActions(vm({}))).toEqual(['mute', 'block']);
    expect(secondaryActions(vm({ muted: true }))).toEqual(['unmute', 'block']);
    expect(secondaryActions(vm({ relation: 'self' }))).toEqual([]);
    // P1: hide-via for friends; showing the paths again for anyone hidden.
    expect(secondaryActions(vm({ relation: 'friend' }))).toEqual(['mute', 'hide_via', 'block']);
    expect(secondaryActions(vm({ relation: 'friend_plus', hidden_via: true }))).toEqual(['mute', 'show_via', 'block']);
    expect(secondaryActions(vm({ hidden_via: true }))).toEqual(['mute', 'show_via', 'block']);
    expect(canUpgrade(vm({ relation: 'friend' }))).toBe(true);
    expect(canUpgrade(vm({ relation: 'friend_plus' }))).toBe(false);
    expect(canUpgrade(vm({}))).toBe(false);
  });

  it('names the co-sign purpose of graph-changing actions', () => {
    const vm = (over: Record<string, unknown>) => toPersonVM(person('bob', over) as never);
    expect(cosignPurpose('request', vm({}))).toBe('friend_request');
    expect(cosignPurpose('accept', vm({ request: 'incoming' }))).toBe('friend_accept');
    expect(cosignPurpose('unfriend', vm({ relation: 'friend' }))).toBe('unfriend');
    expect(cosignPurpose('block', vm({}))).toBe('block');
    // Blocking a friend signs the Revocation the block writes.
    expect(cosignPurpose('block', vm({ relation: 'friend_plus' }))).toBe('unfriend');
    for (const a of ['withdraw', 'decline', 'unblock', 'mute', 'unmute', 'hide_via', 'show_via'] as const) {
      expect(cosignPurpose(a, vm({}))).toBeUndefined();
    }
  });

  it('names errors', () => {
    expect(peopleErrorKey(new ApiError(409, { error: 'friend_cap' }))).toBe('people:errors.friend_cap');
    expect(peopleErrorKey(new ApiError(404, { error: 'not_found' }))).toBe('errors:not_found');
  });
});

describe('people pages', () => {
  it('lists requests and accepts one', async () => {
    let accepted = false;
    const calls = stubAPI((c) => {
      if (c.path === '/friends/requests') {
        return json(200, accepted ? { incoming: [], outgoing: [] } : {
          incoming: [person('alice', { request: 'incoming' })],
          outgoing: [person('dan', { request: 'outgoing' })],
        });
      }
      if (c.method === 'POST' && c.path === '/friends/requests/r-alice/accept') {
        accepted = true;
        return json(204, null);
      }
      return undefined;
    });
    renderAt('/friends/requests', '/friends/requests', <RequestsPage />);
    expect(await screen.findByText('@alice')).toBeTruthy();
    expect(screen.getByText('@dan')).toBeTruthy();
    expect(screen.getByRole('button', { name: tr('people:action.withdraw') })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: tr('people:action.accept') }));
    await waitFor(() => {
      expect(calls.some((c) => c.path === '/friends/requests/r-alice/accept')).toBe(true);
    });
    expect(await screen.findByText(tr('people:requests.incomingEmpty'))).toBeTruthy();
  });

  it('shows a person with a pending request the same way whatever happened to it', async () => {
    stubAPI((c) => (c.path === '/people/r-bob' ? json(200, person('bob', { request: 'outgoing' })) : undefined));
    renderAt('/people/r-bob', '/people/:ref', <PersonPage />);
    expect(await screen.findByText(tr('people:person.relation.outgoing'))).toBeTruthy();
    expect(screen.getByRole('button', { name: tr('people:action.withdraw') })).toBeTruthy();
    expect(screen.getByTestId('integrity-chain').textContent).toContain('@carol');
    expect(screen.getByTestId('integrity-inviter-label')).toBeTruthy();
    expect(screen.getByTestId('integrity-report')).toBeTruthy();
  });

  it('asks before blocking, then blocks', async () => {
    const calls = stubAPI((c) => {
      if (c.path === '/people/r-bob' && c.method === 'GET') return json(200, person('bob', { relation: 'friend' }));
      if (c.path === '/people/r-bob/block' && c.method === 'PUT') return json(204, null);
      return undefined;
    });
    renderAt('/people/r-bob', '/people/:ref', <PersonPage />);
    fireEvent.click(await screen.findByRole('button', { name: tr('people:action.block', { handle: 'bob' }) }));
    expect(calls.some((c) => c.method === 'PUT')).toBe(false);
    expect(screen.getByText(tr('people:confirm.block', { handle: 'bob' }))).toBeTruthy();
    fireEvent.click(screen.getByTestId('confirm-block'));
    await waitFor(() => {
      expect(calls.some((c) => c.method === 'PUT' && c.path === '/people/r-bob/block')).toBe(true);
    });
  });

  it('co-signs a request in a passkey session and sends the signature', async () => {
    cosign.asked = [];
    cosign.result = { intent: 'i-1', assertion: { id: 'cred' } };
    const calls = stubAPI((c) => {
      if (c.path === '/auth/passkeys') return json(200, { items: [{ id: 'p', name: 'x', created_at: '2026-01-01T00:00:00Z', current: true }] });
      if (c.path === '/people/r-bob' && c.method === 'GET') return json(200, person('bob'));
      if (c.path === '/people/r-bob/request' && c.method === 'POST') return json(202, { state: 'outgoing' });
      return undefined;
    });
    renderAt('/people/r-bob', '/people/:ref', <PersonPage />);
    fireEvent.click(await screen.findByRole('button', { name: tr('people:action.request') }));
    await waitFor(() => {
      expect(calls.some((c) => c.method === 'POST' && c.path === '/people/r-bob/request')).toBe(true);
    });
    const sent = calls.find((c) => c.method === 'POST' && c.path === '/people/r-bob/request');
    expect(JSON.parse(sent?.body ?? '{}')).toEqual({ intent: 'i-1', assertion: { id: 'cred' } });
    expect(cosign.asked).toEqual([{ purpose: 'friend_request', target: 'r-bob', summary: tr('people:cosign.request', { handle: 'bob' }) }]);
  });

  it('changes nothing when the co-sign dialog is cancelled; a block of a friend signs the unfriend', async () => {
    cosign.asked = [];
    cosign.result = null;
    const calls = stubAPI((c) => {
      if (c.path === '/auth/passkeys') return json(200, { items: [{ id: 'p', name: 'x', created_at: '2026-01-01T00:00:00Z', current: true }] });
      if (c.path === '/people/r-bob' && c.method === 'GET') return json(200, person('bob', { relation: 'friend' }));
      return undefined;
    });
    renderAt('/people/r-bob', '/people/:ref', <PersonPage />);
    fireEvent.click(await screen.findByRole('button', { name: tr('people:action.block', { handle: 'bob' }) }));
    fireEvent.click(screen.getByTestId('confirm-block'));
    await waitFor(() => {
      expect(cosign.asked).toHaveLength(1);
    });
    expect(cosign.asked[0]).toMatchObject({ purpose: 'unfriend', target: 'r-bob' });
    expect(calls.some((c) => c.method === 'PUT')).toBe(false);
    expect(screen.getByTestId('confirm-block')).toBeTruthy();
  });

  it('hides the paths through a friend and shows them again', async () => {
    let hidden = false;
    const calls = stubAPI((c) => {
      if (c.path === '/people/r-bob' && c.method === 'GET') return json(200, person('bob', { relation: 'friend', hidden_via: hidden }));
      if (c.path === '/people/r-bob/hide-via') {
        hidden = c.method === 'PUT';
        return json(204, null);
      }
      return undefined;
    });
    renderAt('/people/r-bob', '/people/:ref', <PersonPage />);
    fireEvent.click(await screen.findByTestId('action-hide_via'));
    expect(screen.getByText(tr('people:confirm.hide_via', { handle: 'bob' }))).toBeTruthy();
    fireEvent.click(screen.getByTestId('confirm-hide_via'));
    expect(await screen.findByTestId('person-hidden-via')).toBeTruthy();
    expect(calls.some((c) => c.method === 'PUT' && c.path === '/people/r-bob/hide-via')).toBe(true);
    fireEvent.click(await screen.findByTestId('action-show_via'));
    await waitFor(() => {
      expect(screen.queryByTestId('person-hidden-via')).toBeNull();
    });
    expect(calls.some((c) => c.method === 'DELETE' && c.path === '/people/r-bob/hide-via')).toBe(true);
  });

  it('opens the friend+ upgrade handshake for a plain friend only', async () => {
    let rel = 'friend';
    const calls = stubAPI((c) => {
      if (c.path === '/people/r-bob' && c.method === 'GET') return json(200, person('bob', { relation: rel }));
      if (c.path === '/friends/r-bob/upgrade' && c.method === 'POST') {
        return json(201, { hs: 'h-1', mode: 'qr_audio', frame_ttl_ms: 250, frame_key: 'AAAA' });
      }
      return undefined;
    });
    const view = renderAt('/people/r-bob', '/people/:ref', <PersonPage />);
    fireEvent.click(await screen.findByTestId('action-upgrade'));
    expect(await screen.findByTestId('invite-handshake')).toBeTruthy();
    expect(screen.getByText(tr('people:upgrade.hint', { handle: 'bob' }))).toBeTruthy();
    expect(calls.some((c) => c.method === 'POST' && c.path === '/friends/r-bob/upgrade')).toBe(true);
    view.unmount();
    rel = 'friend_plus';
    renderAt('/people/r-bob', '/people/:ref', <PersonPage />);
    expect(await screen.findByTestId('person-relation')).toBeTruthy();
    expect(screen.queryByTestId('action-upgrade')).toBeNull();
  });

  it('shows the not-found page for a person the viewer cannot see', async () => {
    stubAPI(() => undefined);
    renderAt('/people/r-nope', '/people/:ref', <PersonPage />);
    expect(await screen.findByText(tr('common:notFound.title'))).toBeTruthy();
  });

  it('reports an inviter through the invitee', async () => {
    stubAPI((c) => (c.path === '/people/r-bob' ? json(200, person('bob')) : undefined));
    renderAt('/people/r-bob', '/people/:ref', <PersonPage />);
    fireEvent.click(await screen.findByTestId('integrity-report'));
    expect(await screen.findByTestId('report-modal')).toBeTruthy();
  });

  it('pages through friends', async () => {
    stubAPI((c) => {
      if (c.path !== '/friends') return undefined;
      return json(200, { items: [person('fay', { relation: 'friend', chain: { dist: 1, hidden_middle: 0, first_hop_plus: false } })], next: 'c1' });
    });
    renderAt('/friends', '/friends', <FriendsPage />);
    expect((await screen.findAllByText('@fay')).length).toBeGreaterThan(0);
    expect(screen.getByRole('button', { name: tr('people:friends.more') })).toBeTruthy();
  });

  it('shows mutual friend counts on suggestions', async () => {
    stubAPI((c) => (c.path === '/suggestions' ? json(200, { items: [{ person: person('gus'), mutual: 3 }] }) : undefined));
    renderAt('/suggestions', '/suggestions', <SuggestionsPage />);
    expect(await screen.findByText(tr('people:suggestions.mutual', { count: 3 }))).toBeTruthy();
    expect(screen.getByRole('button', { name: tr('people:action.request') })).toBeTruthy();
  });

  it('unblocks from the blocks list', async () => {
    let blocked = true;
    const calls = stubAPI((c) => {
      if (c.path === '/blocks') return json(200, { items: blocked ? [person('hal', { blocked: true, chain: { dist: 0, hidden_middle: 0, first_hop_plus: false } })] : [] });
      if (c.path === '/people/r-hal/block' && c.method === 'DELETE') {
        blocked = false;
        return json(204, null);
      }
      return undefined;
    });
    renderAt('/settings/blocks', '/settings/blocks', <BlocksPage />);
    fireEvent.click(await screen.findByRole('button', { name: tr('people:action.unblock') }));
    await waitFor(() => {
      expect(calls.some((c) => c.method === 'DELETE')).toBe(true);
    });
    expect(await screen.findByText(tr('people:blocks.empty'))).toBeTruthy();
  });
});
