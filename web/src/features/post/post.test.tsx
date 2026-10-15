// Post feature: view-model helpers, language guess and the manage page against
// a stubbed API.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/errors';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import { asRef } from '../../vm/types';
import { defaultLang, langOptions, primarySubtag } from './lang';
import ManagePostPage from './ManagePostPage';
import { me, postJSON } from './testdata';
import { codePoints, counterState, hasAudienceLimit, postErrorKey, toPostVM } from './vm';

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

function stubAPI(handler: (c: Call) => Response | undefined): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const text = await req.clone().text();
      const c = { method: req.method, path: new URL(req.url).pathname.replace(/^\/api/, ''), body: text ? (JSON.parse(text) as unknown) : undefined };
      calls.push(c);
      if (c.path === '/session') return json(200, { authenticated: true, kind: 'user', csrf: 'x', me });
      return handler(c) ?? json(404, { error: 'not_found' });
    }),
  );
  return calls;
}

function renderAt(path: string) {
  const router = createMemoryRouter(
    [
      { path: '/p/:ref/edit', element: <ManagePostPage /> },
      { path: '/p/:ref', element: <p>tree page</p> },
      { path: '/compose', element: <p>composer</p> },
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

describe('post helpers', () => {
  it('counts code points, not UTF-16 units', () => {
    expect(codePoints('😀😀')).toBe(2);
    expect('😀😀'.length).toBe(4);
    expect(codePoints('é')).toBe(2);
    expect(codePoints('\ud83d')).toBe(1); // a lone surrogate is one code point
  });

  it('shows the counter from 280, amber past 500, over past 20,000', () => {
    expect(counterState(279)).toBe('hidden');
    expect(counterState(280)).toBe('normal');
    expect(counterState(500)).toBe('normal');
    expect(counterState(501)).toBe('amber');
    expect(counterState(20000)).toBe('amber');
    expect(counterState(20001)).toBe('over');
  });

  it('maps a PostView and flags audience limits', () => {
    const p = toPostVM(postJSON({ audience: 'd1', edited_at: '2026-01-02T00:00:00Z', thread: { index: 1, total: 2 } }) as never);
    expect(p.ref).toBe(asRef('p1'));
    expect(p.body?.source).toBe('hello world');
    expect(p.editedAt?.toISOString()).toBe('2026-01-02T00:00:00.000Z');
    expect(p.thread).toEqual({ index: 1, total: 2 });
    expect(hasAudienceLimit(p)).toBe(true);
    expect(hasAudienceLimit(toPostVM(postJSON() as never))).toBe(false);
    expect(hasAudienceLimit(toPostVM(postJSON({ audience_forced: true }) as never))).toBe(true);
  });

  it('names posts errors', () => {
    expect(postErrorKey(new ApiError(409, { error: 'ref_cap' }))).toBe('post:errors.ref_cap');
    expect(postErrorKey(new ApiError(404, { error: 'not_found' }))).toBe('errors:not_found');
  });

  it('guesses the post language from languages[], then the UI locale', () => {
    expect(defaultLang(['pt-BR', 'en'], 'en-US')).toBe('pt-BR');
    expect(defaultLang([], 'fr-CA')).toBe('fr');
    expect(defaultLang(undefined, undefined)).toBe('und');
    expect(primarySubtag('zh_Hant')).toBe('zh');
    const opts = langOptions(['de', 'en'], 'ja');
    expect(opts.slice(0, 3)).toEqual(['de', 'en', 'ja']);
    expect(opts[opts.length - 1]).toBe('und');
    expect(new Set(opts).size).toBe(opts.length);
  });
});

describe('manage post page', () => {
  it('edits the post as a new version', async () => {
    const calls = stubAPI((c) => {
      if (c.method === 'GET' && c.path === '/posts/p1') return json(200, postJSON());
      if (c.method === 'PATCH' && c.path === '/posts/p1') {
        return json(200, postJSON({ body: 'hello again', edited_at: '2026-01-02T00:00:00Z' }));
      }
      return undefined;
    });
    renderAt('/p/p1/edit');
    const box = await screen.findByTestId('post-edit-body');
    expect((box as HTMLTextAreaElement).value).toBe('hello world');
    fireEvent.change(box, { target: { value: 'hello again' } });
    fireEvent.click(screen.getByTestId('post-edit-save'));
    await screen.findByTestId('post-edit-saved');
    expect(calls.find((c) => c.method === 'PATCH')?.body).toEqual({ body: 'hello again', lang: 'en' });
    await waitFor(() => {
      expect(screen.getByTestId('integrity-edited').hidden).toBe(false);
    });
  });

  it('offers detach-all when the post turns slippery', async () => {
    const calls = stubAPI((c) => {
      if (c.method === 'GET' && c.path === '/posts/p1') return json(200, postJSON({ reply_count: 2 }));
      if (c.method === 'PUT' && c.path === '/posts/p1/ref-policy') return json(200, { offer_detach_all: true });
      if (c.method === 'POST' && c.path === '/posts/p1/detach-all') return json(204, null);
      return undefined;
    });
    renderAt('/p/p1/edit');
    fireEvent.change(await screen.findByTestId('post-policy'), { target: { value: 'nobody' } });
    fireEvent.click(screen.getByTestId('post-policy-save'));
    expect(await screen.findByText(tr('post:policyForm.offer'))).toBeTruthy();
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ ref_policy: 'nobody' });
    fireEvent.click(screen.getByTestId('post-detach-all'));
    await screen.findByTestId('post-detached');
    expect(calls.some((c) => c.method === 'POST' && c.path === '/posts/p1/detach-all')).toBe(true);
  });

  it('deletes after confirmation', async () => {
    const calls = stubAPI((c) => {
      if (c.method === 'GET' && c.path === '/posts/p1') return json(200, postJSON());
      if (c.method === 'DELETE' && c.path === '/posts/p1') return json(204, null);
      return undefined;
    });
    renderAt('/p/p1/edit');
    fireEvent.click(await screen.findByTestId('post-delete'));
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
    fireEvent.click(screen.getByTestId('post-delete-confirm'));
    await screen.findByTestId('post-deleted');
    expect(calls.some((c) => c.method === 'DELETE' && c.path === '/posts/p1')).toBe(true);
  });

  it('shows errors from the post catalog', async () => {
    stubAPI((c) => {
      if (c.method === 'GET' && c.path === '/posts/p1') return json(200, postJSON());
      if (c.method === 'PATCH') return json(422, { error: 'body_too_long', field: 'body' });
      return undefined;
    });
    renderAt('/p/p1/edit');
    fireEvent.click(await screen.findByTestId('post-edit-save'));
    expect(await screen.findByText(tr('post:errors.body_too_long'))).toBeTruthy();
  });

  it("is not found for someone else's post and for unknown refs", async () => {
    stubAPI((c) => (c.path === '/posts/p2' ? json(200, postJSON({ ref: 'p2', mine: false })) : undefined));
    renderAt('/p/p2/edit');
    expect(await screen.findByText(tr('common:notFound.title'))).toBeTruthy();
    expect(screen.queryByTestId('post-edit-body')).toBeNull();
  });
});
