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
import { peopleErrorKey, primaryActions, reportPath, secondaryActions, toPersonVM } from './vm';

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
    vi.fn((req: Request) => {
      const c = { method: req.method, path: new URL(req.url).pathname.replace(/^\/api/, '') };
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
      { path: pattern, element: el },
      { path: '/report', element: <p>report page</p> },
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
    });
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
  });

  it('names errors and report paths', () => {
    expect(peopleErrorKey(new ApiError(409, { error: 'friend_cap' }))).toBe('people:errors.friend_cap');
    expect(peopleErrorKey(new ApiError(404, { error: 'not_found' }))).toBe('errors:not_found');
    expect(reportPath('inviter', 'r-x')).toBe('/report?kind=inviter&ref=r-x');
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

  it('shows the not-found page for a person the viewer cannot see', async () => {
    stubAPI(() => undefined);
    renderAt('/people/r-nope', '/people/:ref', <PersonPage />);
    expect(await screen.findByText(tr('common:notFound.title'))).toBeTruthy();
  });

  it('reports an inviter through the invitee', async () => {
    stubAPI((c) => (c.path === '/people/r-bob' ? json(200, person('bob')) : undefined));
    renderAt('/people/r-bob', '/people/:ref', <PersonPage />);
    fireEvent.click(await screen.findByTestId('integrity-report'));
    expect(await screen.findByText('report page')).toBeTruthy();
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
