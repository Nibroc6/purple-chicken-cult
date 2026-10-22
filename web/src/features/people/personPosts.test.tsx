// A person's post history: newest first by default, page by page, the order
// toggle (a cursor is only sent for the order it came from), the empty state.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import { resetEngageStore } from '../engage/store';
import { postJSON } from '../post/testdata';
import PersonPage from './PersonPage';

function json(status: number, body: unknown): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const bob = {
  ref: 'r-bob',
  handle: 'bob',
  display_name: 'Bob',
  inviter: { kind: 'genesis' },
  relation: 'friend',
  chain: { dist: 1, hidden_middle: 0, first_hop_plus: false },
};

// stubAPI answers by path + query and records the posts requests.
function stubAPI(handler: (path: string) => Response | undefined): string[] {
  const calls: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn((req: Request) => {
      const u = new URL(req.url);
      const path = u.pathname.replace(/^\/api/, '') + u.search;
      if (path.startsWith('/people/r-bob/posts')) calls.push(path);
      if (path === '/session') return Promise.resolve(json(200, { authenticated: true, kind: 'user', csrf: 'x' }));
      if (path === '/people/r-bob') return Promise.resolve(json(200, bob));
      return Promise.resolve(handler(path) ?? json(404, { error: 'not_found' }));
    }),
  );
  return calls;
}

function renderPage() {
  const router = createMemoryRouter([{ path: '/people/:ref', element: <PersonPage /> }], { initialEntries: ['/people/r-bob'] });
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;
const post = (ref: string, body: string) => postJSON({ ref, body, mine: false });

beforeEach(() => {
  resetEngageStore();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('person post history', () => {
  it('lists newest first, pages on, and switches to oldest first', async () => {
    const calls = stubAPI((path) => {
      switch (path) {
        case '/people/r-bob/posts?order=newest':
          return json(200, { items: [post('p3', 'third'), post('p2', 'second')], next: 'n1' });
        case '/people/r-bob/posts?cursor=n1':
          return json(200, { items: [post('p1', 'first')] });
        case '/people/r-bob/posts?order=oldest':
          return json(200, { items: [post('p1', 'first'), post('p2', 'second'), post('p3', 'third')] });
      }
      return undefined;
    });
    renderPage();
    expect(await screen.findAllByTestId('person-post')).toHaveLength(2);
    expect(screen.getByTestId('person-posts-newest').getAttribute('aria-pressed')).toBe('true');
    fireEvent.click(screen.getByTestId('person-posts-more'));
    expect((await screen.findByText('first')).closest('[data-testid="person-post"]')?.getAttribute('data-ref')).toBe('p1');
    expect(screen.queryByTestId('person-posts-more')).toBeNull();

    fireEvent.click(screen.getByTestId('person-posts-oldest'));
    await waitFor(() => {
      expect(screen.getAllByTestId('person-post').map((li) => li.getAttribute('data-ref'))).toEqual(['p1', 'p2', 'p3']);
    });
    expect(screen.getByTestId('person-posts-oldest').getAttribute('aria-pressed')).toBe('true');
    expect(calls).toEqual(['/people/r-bob/posts?order=newest', '/people/r-bob/posts?cursor=n1', '/people/r-bob/posts?order=oldest']);
  });

  it('says so when there is nothing to show', async () => {
    stubAPI((path) => (path.startsWith('/people/r-bob/posts') ? json(200, { items: [] }) : undefined));
    renderPage();
    expect(await screen.findByText(tr('people:posts.empty', { handle: 'bob' }))).toBeTruthy();
    expect(screen.queryByTestId('person-posts-more')).toBeNull();
  });
});
