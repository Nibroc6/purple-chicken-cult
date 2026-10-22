// Saves feature: the saves page (most recent first, page by page, an empty
// state) and an unsave that keeps the post listed so it can be saved again.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import { resetEngageStore } from '../engage/store';
import { postJSON } from '../post/testdata';
import SavesPage from './SavesPage';

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
      const u = new URL(req.url);
      const c = { method: req.method, path: u.pathname.replace(/^\/api/, '') + u.search };
      calls.push(c);
      return Promise.resolve(handler(c) ?? json(404, { error: 'not_found' }));
    }),
  );
  return calls;
}

function renderPage() {
  const router = createMemoryRouter([{ path: '/saves', element: <SavesPage /> }], { initialEntries: ['/saves'] });
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  resetEngageStore();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('saves page', () => {
  it('lists saved posts page by page', async () => {
    const calls = stubAPI((c) => {
      if (c.path === '/saves') {
        return json(200, {
          items: [postJSON({ ref: 's1', mine: false, saved_by_me: true }), postJSON({ ref: 's2', mine: false, saved_by_me: true })],
          next: 'c1',
        });
      }
      if (c.path === '/saves?cursor=c1') return json(200, { items: [postJSON({ ref: 's3', mine: false, body: 'third', saved_by_me: true })] });
      return undefined;
    });
    renderPage();
    expect(await screen.findAllByTestId('saves-item')).toHaveLength(2);
    fireEvent.click(screen.getByTestId('saves-more'));
    expect((await screen.findByText('third')).closest('[data-testid="saves-item"]')?.getAttribute('data-ref')).toBe('s3');
    expect(screen.queryByTestId('saves-more')).toBeNull();
    // (The cards also read the session and account for the reader's format.)
    expect(calls.map((c) => c.path).filter((p) => p.startsWith('/saves'))).toEqual(['/saves', '/saves?cursor=c1']);
    expect(screen.getAllByTestId('engage-save').every((b) => b.getAttribute('aria-pressed') === 'true')).toBe(true);
  });

  it('keeps an unsaved post listed until the page reloads', async () => {
    const calls = stubAPI((c) => {
      if (c.path === '/saves') return json(200, { items: [postJSON({ ref: 's1', mine: false, saved_by_me: true })] });
      if (c.path === '/posts/s1/save') return json(204, null);
      return undefined;
    });
    renderPage();
    fireEvent.click(await screen.findByTestId('engage-save'));
    await waitFor(() => {
      expect(calls.some((c) => c.method === 'DELETE' && c.path === '/posts/s1/save')).toBe(true);
    });
    expect(screen.getAllByTestId('saves-item')).toHaveLength(1);
    expect(screen.getByTestId('engage-save').getAttribute('aria-pressed')).toBe('false');
    expect(calls.filter((c) => c.path === '/saves')).toHaveLength(1);
  });

  it('says when nothing was saved', async () => {
    stubAPI((c) => (c.path === '/saves' ? json(200, { items: [] }) : undefined));
    renderPage();
    expect((await screen.findByTestId('saves-empty')).textContent).toBe(i18n.t('saves:page.empty'));
  });
});
