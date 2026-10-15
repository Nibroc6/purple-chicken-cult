// Moderator recovery page against a stubbed API.
import { QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/errors';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import { isRef, recoveryErrorKey } from './api';
import ModRecoveryPage from './ModRecoveryPage';

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

function stub(role: string, handler: (method: string, path: string, body: unknown) => Response | undefined) {
  const seen: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname.replace(/^\/api/, '');
      const text = await req.text();
      seen.push(`${req.method} ${path}`);
      if (path === '/session') {
        return json(200, {
          authenticated: true,
          kind: 'user',
          csrf: 'x',
          me: { ref: 'm', handle: 'mod', display_name: '', role, inviter: { kind: 'genesis' }, settings: {} },
        });
      }
      return handler(req.method, path, text ? (JSON.parse(text) as unknown) : undefined) ?? json(404, { error: 'not_found' });
    }),
  );
  return seen;
}

function renderPage() {
  const router = createMemoryRouter([{ path: '/mod/recovery', element: <ModRecoveryPage /> }], {
    initialEntries: ['/mod/recovery'],
  });
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('moderator recovery', () => {
  it('validates refs and maps recovery problems', () => {
    expect(isRef('AbC-_9')).toBe(true);
    expect(isRef('a b')).toBe(false);
    expect(isRef('')).toBe(false);
    expect(recoveryErrorKey(new ApiError(409, { error: 'recovery_held' }))).toBe('modrecovery:errors.recovery_held');
    expect(recoveryErrorKey(new ApiError(404, { error: 'not_found' }))).toBe('errors:not_found');
  });

  it('is for moderators only', async () => {
    const seen = stub('member', () => undefined);
    renderPage();
    expect(await screen.findByText(i18n.t('modrecovery:page.membersOnly'))).toBeTruthy();
    expect(seen.filter((s) => s.includes('/mod/'))).toEqual([]);
  });

  it('opens, approves and fetches the link', async () => {
    let approvals = 0;
    stub('moderator', (method, path, body) => {
      if (method === 'POST' && path === '/mod/recovery') {
        expect(body).toEqual({ subject: 'subjectRef' });
        return json(201, { id: 'req123' });
      }
      if (method === 'POST' && path === '/mod/recovery/req123/approve') {
        approvals++;
        return approvals === 1
          ? json(409, { error: 'second_moderator_required' })
          : new Response(null, { status: 204 });
      }
      if (method === 'GET' && path === '/mod/recovery/req123/link') {
        return json(200, { enroll_url: 'https://pcc.example/enroll#tok' });
      }
      return undefined;
    });
    renderPage();
    const subject = await screen.findByLabelText(i18n.t('modrecovery:request.subject'));
    const openBtn = screen.getByRole('button', { name: i18n.t('modrecovery:request.button') });
    fireEvent.change(subject, { target: { value: 'not a ref' } });
    expect((openBtn as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(subject, { target: { value: 'subjectRef' } });
    fireEvent.click(openBtn);
    expect(await screen.findByText('req123')).toBeTruthy();

    const approveId = screen.getAllByLabelText(i18n.t('modrecovery:approve.id'))[0];
    if (!approveId) throw new Error('no approve field');
    fireEvent.change(approveId, { target: { value: 'req123' } });
    const approveBtn = screen.getByRole('button', { name: i18n.t('modrecovery:approve.button') });
    fireEvent.click(approveBtn);
    expect(await screen.findByText(i18n.t('modrecovery:errors.second_moderator_required'))).toBeTruthy();
    fireEvent.click(approveBtn);
    expect(await screen.findByText(i18n.t('modrecovery:approve.done'))).toBeTruthy();

    const linkId = screen.getAllByLabelText(i18n.t('modrecovery:link.id'))[1];
    if (!linkId) throw new Error('no link field');
    fireEvent.change(linkId, { target: { value: 'req123' } });
    fireEvent.click(screen.getByRole('button', { name: i18n.t('modrecovery:link.button') }));
    expect(await screen.findByText('https://pcc.example/enroll#tok')).toBeTruthy();
  });
});
