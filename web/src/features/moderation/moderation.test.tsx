// Moderation feature against a stubbed API: the report page (query params,
// closed reasons, note counter, one answer whatever the target, errors), the
// blame inbox, the moderator queue (members see nothing, remove_post only for
// posts, two-person suspend with the second moderator's confirm, error codes),
// the public log (names vs pseudonyms), the footer slot and a pseudo-locale pass.
// P1-W06: the unified queue (csam holds without content, signals, claims), the
// co-signed confirmation of a passkey session, the csam report and quarantine.
import { QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterAll, afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/errors';
import { makeQueryClient } from '../../app/App';
import { requestCosign } from '../cosign';
import { i18n } from '../../i18n/setup';
import {
  actionsFor,
  canQuarantine,
  codePoints,
  reportRefOK,
  defaultReason,
  isRef,
  isTargetKind,
  modReasons,
  moderationErrorKey,
  reportReasons,
  type BlameItem,
  type LogItem,
  type ModItem,
} from './api';
import BlamePage from './BlamePage';
import ModLogPage from './ModLogPage';
import ModQueuePage from './ModQueuePage';
import ReportPage from './ReportPage';
import { ModerationFooter } from './slots';

vi.mock('../cosign', () => ({ requestCosign: vi.fn() }));

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

interface Call {
  method: string;
  path: string;
  query: string;
  body: unknown;
}

// stub answers /session for role (or signed out when role is undefined) and
// hands every other call to handler.
function stub(role: string | undefined, handler: (c: Call) => Response | undefined): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      const path = u.pathname.replace(/^\/api/, '');
      const text = await req.text();
      const c: Call = { method: req.method, path, query: u.search, body: text ? (JSON.parse(text) as unknown) : undefined };
      if (path === '/session') {
        if (!role) return json(200, { authenticated: false, kind: 'none' });
        return json(200, {
          authenticated: true,
          kind: 'user',
          csrf: 'x',
          me: { ref: 'me', handle: 'me', display_name: '', role, inviter: { kind: 'genesis' }, settings: {} },
        });
      }
      calls.push(c);
      return handler(c) ?? json(404, { error: 'not_found' });
    }),
  );
  return calls;
}

function renderAt(path: string, element: ReactNode, route = path.split('?')[0] ?? path) {
  const router = createMemoryRouter(
    [
      { path: route, element },
      { path: '/', element: <p data-testid="home">home</p> },
    ],
    { initialEntries: ['/', path], initialIndex: 1 },
  );
  render(
    <QueryClientProvider client={makeQueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('helpers', () => {
  it('validates targets and maps errors', () => {
    expect(isTargetKind('post')).toBe(true);
    expect(isTargetKind('inviter')).toBe(true);
    expect(isTargetKind('account')).toBe(false);
    expect(isTargetKind(null)).toBe(false);
    expect(isRef('AbC-_9')).toBe(true);
    expect(isRef('a b')).toBe(false);
    expect(isRef('x'.repeat(257))).toBe(false);
    expect(defaultReason('preview')).toBe('misleading_preview');
    expect(defaultReason('post')).toBe('spam');
    expect(codePoints('a😀é')).toBe(3);
    expect(reportReasons).not.toContain('ban_evasion');
    expect(modReasons).toContain('ban_evasion');
    expect(moderationErrorKey(new ApiError(409, { error: 'second_moderator_required' }))).toBe(
      'moderation:errors.second_moderator_required',
    );
    expect(moderationErrorKey(new ApiError(429, { error: 'rate_limited' }))).toBe('errors:rate_limited');
  });
});

describe('report page', () => {
  it('refuses links without a valid kind and ref', async () => {
    stub('member', () => undefined);
    renderAt('/report?kind=account&ref=abc', <ReportPage />);
    expect(await screen.findByTestId('report-invalid')).toBeTruthy();
    expect(screen.queryByTestId('report-form')).toBeNull();
  });

  it('sends the closed reason and the note, and says the same thing whatever the target', async () => {
    const calls = stub('member', (c) => (c.path === '/reports' ? new Response(null, { status: 202 }) : undefined));
    renderAt('/report?kind=inviter&ref=inviteeRef', <ReportPage />);
    expect(await screen.findByText(tr('moderation:report.heading.inviter'))).toBeTruthy();
    const radios = screen.getAllByRole('radio');
    expect(radios.map((r) => (r as HTMLInputElement).value)).toEqual([...reportReasons]);
    expect(screen.getByDisplayValue<HTMLInputElement>('bot').checked).toBe(true);
    fireEvent.click(screen.getByDisplayValue('harassment'));
    fireEvent.change(screen.getByLabelText(tr('moderation:report.note')), { target: { value: '  keeps messaging me  ' } });
    fireEvent.click(screen.getByTestId('report-submit'));
    expect(await screen.findByTestId('report-sent')).toBeTruthy();
    expect(calls).toEqual([
      {
        method: 'POST',
        path: '/reports',
        query: '',
        body: { target: { kind: 'inviter', ref: 'inviteeRef' }, reason: 'harassment', note: 'keeps messaging me' },
      },
    ]);
  });

  it('counts the note in code points and blocks an over-long one', async () => {
    stub('member', () => undefined);
    renderAt('/report?kind=post&ref=p1', <ReportPage />);
    const note = await screen.findByLabelText(tr('moderation:report.note'));
    fireEvent.change(note, { target: { value: '😀'.repeat(1000) } });
    expect(screen.getByTestId('report-count').textContent).toBe(tr('moderation:report.count', { count: 1000, max: 1000 }));
    expect(screen.getByTestId<HTMLButtonElement>('report-submit').disabled).toBe(false);
    fireEvent.change(note, { target: { value: '😀'.repeat(1001) } });
    expect(screen.getByTestId<HTMLButtonElement>('report-submit').disabled).toBe(true);
  });

  it('shows rate limits with the wait time', async () => {
    stub('member', (c) => (c.path === '/reports' ? json(429, { error: 'rate_limited', retry_after_s: 30 }) : undefined));
    renderAt('/report?kind=post&ref=p1', <ReportPage />);
    fireEvent.click(await screen.findByTestId('report-submit'));
    expect((await screen.findByTestId('report-error')).textContent).toBe(tr('errors:rate_limited', { seconds: 30 }));
  });

  it('goes back on cancel', async () => {
    stub('member', () => undefined);
    renderAt('/report?kind=person&ref=u1', <ReportPage />);
    fireEvent.click(await screen.findByRole('button', { name: tr('moderation:report.cancel') }));
    expect(await screen.findByTestId('home')).toBeTruthy();
  });
});

const blameItems: BlameItem[] = [
  {
    id: 'b1',
    action: 'remove_post',
    reason: 'spam',
    invitee_handle: 'bob',
    at: new Date().toISOString(),
    read: false,
    post: {
      ref: 'p1',
      author: { ref: 'u1', handle: 'bob', display_name: 'Bob', inviter: { kind: 'user', handle: 'me' }, chain: { dist: 1, hidden_middle: 0, first_hop_plus: false } },
      tier: 1,
      body: '**buy** now',
      lang: 'en',
      created_at: new Date().toISOString(),
      audience: 'connected',
      ref_policy: 'nobody',
      can_reference: false,
      mine: false,
      likes: { in_network: 0, others: 0, by_me: false },
      reply_count: 0,
    },
  },
  { id: 'b2', action: 'suspend_user', reason: 'bot', invitee_handle: 'eve', at: new Date().toISOString(), read: true },
];

describe('blame inbox', () => {
  it('lists actions with reasons and the post only when it came along', async () => {
    stub('member', (c) => (c.path === '/blame' ? json(200, { items: blameItems }) : undefined));
    renderAt('/blame', <BlamePage />);
    const items = await screen.findAllByTestId('blame-item');
    expect(items).toHaveLength(2);
    const [first, second] = items as [HTMLElement, HTMLElement];
    expect(first.dataset.read).toBe('false');
    expect(within(first).getByText(tr('moderation:blame.item', { action: 'remove_post', handle: 'bob' }))).toBeTruthy();
    expect(within(first).getByTestId('blame-reason').textContent).toBe(
      tr('moderation:blame.reason', { reason: tr('moderation:reason.spam') }),
    );
    // The post body is user content, shown as text (never as HTML).
    expect(within(first).getByTestId('blame-post').querySelector('[data-user-content]')?.textContent).toBe('**buy** now');
    expect(within(second).queryByTestId('blame-post')).toBeNull();
    expect(within(second).getByText(tr('moderation:blame.item', { action: 'suspend_user', handle: 'eve' }))).toBeTruthy();
  });

  it('says when it is empty', async () => {
    stub('member', (c) => (c.path === '/blame' ? json(200, { items: [] }) : undefined));
    renderAt('/blame', <BlamePage />);
    expect(await screen.findByTestId('blame-empty')).toBeTruthy();
  });
});

function modItem(over: Partial<ModItem>): ModItem {
  return {
    id: 'i1',
    source: 'report',
    priority: 2,
    reason: 'spam',
    subject_probation: false,
    at: new Date().toISOString(),
    claimed_by_me: false,
    awaiting_second: false,
    ...over,
  };
}

describe('moderator queue', () => {
  it('is for moderators only', async () => {
    const calls = stub('member', () => undefined);
    renderAt('/mod', <ModQueuePage />);
    expect(await screen.findByText(tr('moderation:queue.membersOnly'))).toBeTruthy();
    expect(calls).toEqual([]);
  });

  it('shows subject, inviter, probation and content, and resolves', async () => {
    const post = blameItems[0]?.post;
    let resolved = 0;
    const calls = stub('moderator', (c) => {
      if (c.path === '/mod/queue') {
        return json(200, {
          items:
            resolved > 0
              ? []
              : [
                  modItem({
                    id: 'i1',
                    subject: { ref: 's1', handle: 'bob' },
                    subject_inviter: { kind: 'user', handle: 'alice' },
                    subject_probation: true,
                    post,
                    summary: { reports: 2, kind_post: 1 },
                  }),
                  modItem({ id: 'i2', reason: 'csam', priority: 0, subject: { ref: 's2', handle: 'carl' }, summary: { reports: 1, kind_post: 1 } }),
                  modItem({ id: 'i3', reason: 'bot', subject: { ref: 's3', handle: 'dan' }, subject_inviter: { kind: 'genesis' }, summary: { reports: 1, kind_person: 1 } }),
                ],
        });
      }
      if (c.path === '/mod/items/i1/resolve') {
        resolved++;
        return new Response(null, { status: 204 });
      }
      return undefined;
    });
    renderAt('/mod', <ModQueuePage />);
    const items = await screen.findAllByTestId('queue-item');
    expect(items).toHaveLength(3);
    const [spam, csam, bot] = items as [HTMLElement, HTMLElement, HTMLElement];
    expect(within(spam).getByTestId('queue-subject').textContent).toContain(tr('moderation:queue.subject', { handle: 'bob' }));
    expect(within(spam).getByTestId('queue-inviter').textContent).toBe(tr('moderation:queue.inviter', { kind: 'user', handle: 'alice' }));
    expect(within(spam).getByTestId('queue-probation')).toBeTruthy();
    // The subject's moderator-scoped ref, for the recovery page (INT-0).
    expect(within(spam).getByTestId('queue-subject-ref').textContent).toBe('s1');
    expect(within(bot).getByTestId('queue-subject-ref').textContent).toBe('s3');
    expect(within(spam).getByTestId('queue-post').textContent).toBe('**buy** now');
    expect(within(spam).getByText(tr('moderation:queue.reports', { count: 2 }))).toBeTruthy();
    expect(within(csam).queryByTestId('queue-post')).toBeNull();
    expect(within(csam).getByText(tr('moderation:queue.noContent'))).toBeTruthy();
    // remove_post only where there is a post.
    const options = (el: HTMLElement) =>
      [...within(el).getByTestId<HTMLSelectElement>('queue-action').options].map((o) => o.value);
    expect(options(spam)).toEqual(['remove_post', 'suspend_user', 'dismiss']);
    expect(options(bot)).toEqual(['suspend_user', 'dismiss']);
    expect(within(bot).getByTestId('queue-inviter').textContent).toBe(tr('moderation:queue.inviter', { kind: 'genesis' }));

    fireEvent.change(within(spam).getByTestId('queue-reason'), { target: { value: 'harassment' } });
    fireEvent.click(within(spam).getByTestId('queue-resolve'));
    await waitFor(() => {
      expect(calls.find((c) => c.path === '/mod/items/i1/resolve')?.body).toEqual({ action: 'remove_post', reason: 'harassment' });
    });
    expect(await screen.findByTestId('queue-empty')).toBeTruthy();
  });

  it('runs the two-person suspension and maps its errors', async () => {
    let confirms = 0;
    stub('moderator', (c) => {
      if (c.path === '/mod/queue') {
        return json(200, { items: [modItem({ id: 'i9', reason: 'bot', awaiting_second: true, subject: { ref: 's', handle: 'bob' } })] });
      }
      if (c.path === '/mod/items/i9/confirm') {
        confirms++;
        if (confirms === 1) return json(409, { error: 'second_moderator_required' });
        if (confirms === 2) return json(401, { error: 'step_up_required' });
        return new Response(null, { status: 204 });
      }
      return undefined;
    });
    renderAt('/mod', <ModQueuePage />);
    const item = await screen.findByTestId('queue-item');
    expect(item.dataset.awaiting).toBe('true');
    expect(within(item).queryByTestId('queue-resolve')).toBeNull();
    const confirm = within(item).getByTestId('queue-confirm');
    fireEvent.click(confirm);
    expect((await within(item).findByTestId('queue-error')).textContent).toBe(tr('moderation:errors.second_moderator_required'));
    fireEvent.click(confirm);
    await waitFor(() => {
      expect(within(item).getByTestId('queue-error').textContent).toBe(tr('errors:step_up_required'));
    });
    fireEvent.click(confirm);
    await waitFor(() => {
      expect(confirms).toBe(3);
    });
  });

  it('explains that suspensions need a second moderator', async () => {
    stub('admin', (c) =>
      c.path === '/mod/queue' ? json(200, { items: [modItem({ id: 'i4', reason: 'bot', subject: { ref: 's', handle: 'x' } })] }) : undefined,
    );
    renderAt('/mod', <ModQueuePage />);
    const item = await screen.findByTestId('queue-item');
    fireEvent.change(within(item).getByTestId('queue-action'), { target: { value: 'suspend_user' } });
    expect(within(item).getByText(tr('moderation:queue.twoPerson'))).toBeTruthy();
  });
});

describe('unified queue (P1)', () => {
  const hold = {
    match_info: { driver: 'photodna', match: 'known' },
    source_url: 'https://example.test/img.png',
    held_at: '2026-10-01T10:00:00Z',
    retain_until: '2027-10-01T10:00:00Z',
    account_held: true,
  };

  it('shows a csam hold by its metadata only, never content or a link', async () => {
    stub('moderator', (c) =>
      c.path === '/mod/queue'
        ? json(200, {
            items: [
              modItem({ id: 'c1', source: 'csam', reason: 'csam', priority: 0, subject: { ref: 's', handle: 'bob' }, csam: hold }),
            ],
          })
        : undefined,
    );
    renderAt('/mod', <ModQueuePage />);
    const item = await screen.findByTestId('queue-item');
    expect(item.dataset.source).toBe('csam');
    const panel = within(item).getByTestId('queue-csam');
    expect(panel.textContent).toContain('photodna');
    expect(within(item).getByTestId('queue-csam-url').textContent).toBe(hold.source_url);
    expect(item.querySelector('a, img, video')).toBeNull();
    expect(within(item).queryByTestId('queue-post')).toBeNull();
    expect(within(item).getByTestId('queue-csam-reported').textContent).toBe(tr('moderation:csam.notReported'));
    expect(within(item).getByText(tr('moderation:source.csam'))).toBeTruthy();
  });

  it('starts the csam report with a valid reference only', async () => {
    const calls = stub('moderator', (c) => {
      if (c.path === '/mod/queue') {
        return json(200, { items: [modItem({ id: 'c1', source: 'csam', reason: 'csam', priority: 0, csam: hold })] });
      }
      if (c.path === '/mod/items/c1/csam-report') return new Response(null, { status: 204 });
      return undefined;
    });
    renderAt('/mod', <ModQueuePage />);
    const item = await screen.findByTestId('queue-item');
    const button = within(item).getByTestId<HTMLButtonElement>('queue-csam-report');
    fireEvent.change(within(item).getByTestId('queue-csam-ref'), { target: { value: 'not valid!' } });
    expect(button.disabled).toBe(true);
    fireEvent.change(within(item).getByTestId('queue-csam-ref'), { target: { value: ' CT-123456 ' } });
    fireEvent.click(button);
    await waitFor(() => {
      expect(calls.find((c) => c.path === '/mod/items/c1/csam-report')?.body).toEqual({ report_ref: 'CT-123456' });
    });
    // A csam hold offers dismiss (a false match, two-person) and suspension, no removal.
    expect([...within(item).getByTestId<HTMLSelectElement>('queue-action').options].map((o) => o.value)).toEqual([
      'suspend_user',
      'dismiss',
    ]);
  });

  it('shows signals with their facts, claims, and the quarantine once the tree qualifies', async () => {
    const calls = stub('moderator', (c) => {
      if (c.path === '/mod/queue') {
        return json(200, {
          items: [
            modItem({
              id: 'g1',
              source: 'signal',
              reason: 'cadence',
              subject: { ref: 's', handle: 'bot' },
              subject_inviter: { kind: 'user', handle: 'host' },
              summary: { cadence: 1, posts: 20, hours: 18, subtree_confirmed: 3, quarantine_k: 3, inviter_marks: 1 },
            }),
            modItem({
              id: 'g2',
              source: 'invite_cluster',
              reason: 'invite_cluster',
              claimed: true,
              subject: { ref: 's2', handle: 'joiner' },
              summary: { invite_cluster: 1, accounts: 4, shared_network: 1, subtree_confirmed: 1, quarantine_k: 3 },
            }),
          ],
        });
      }
      if (c.path === '/mod/items/g1/claim' || c.path === '/mod/quarantine') return new Response(null, { status: 204 });
      return undefined;
    });
    renderAt('/mod', <ModQueuePage />);
    const [sig, cluster] = (await screen.findAllByTestId('queue-item')) as [HTMLElement, HTMLElement];
    expect(within(sig).getByText(tr('moderation:signal.cadence'))).toBeTruthy();
    const facts = within(sig).getByTestId('queue-facts');
    expect(facts.textContent).toContain(tr('moderation:fact.posts', { count: 20 }));
    expect(facts.textContent).toContain(tr('moderation:fact.inviter_marks', { count: 1 }));
    expect(within(sig).getByTestId('queue-subtree').textContent).toBe(tr('moderation:fact.subtree_confirmed', { count: 3, k: 3 }));
    expect(within(cluster).getByTestId('queue-claimed').textContent).toBe(tr('moderation:queue.claimedByOther'));
    expect(within(cluster).queryByTestId('queue-quarantine')).toBeNull();
    fireEvent.click(within(sig).getByTestId('queue-claim'));
    fireEvent.click(within(sig).getByTestId('queue-quarantine'));
    await waitFor(() => {
      expect(calls.find((c) => c.path === '/mod/quarantine')?.body).toEqual({ subject_item: 'g1' });
    });
    expect(calls.some((c) => c.path === '/mod/items/g1/claim')).toBe(true);
  });

  it('co-signs the confirmation in a passkey session and sends nothing when cancelled', async () => {
    const cosign = vi.mocked(requestCosign);
    cosign.mockResolvedValueOnce(null).mockResolvedValueOnce({ intent: 'in1', assertion: { id: 'cred' } });
    const calls = stub('moderator', (c) => {
      if (c.path === '/auth/passkeys') return json(200, { items: [{ ref: 'k', current: true }] });
      if (c.path === '/mod/queue') {
        return json(200, {
          items: [
            modItem({
              id: 'p1',
              reason: 'bot',
              awaiting_second: true,
              pending: { action: 'suspend_user', reason: 'bot', mine: false, at: new Date().toISOString() },
              subject: { ref: 's', handle: 'bob' },
            }),
          ],
        });
      }
      if (c.path === '/mod/items/p1/confirm') return new Response(null, { status: 204 });
      return undefined;
    });
    renderAt('/mod', <ModQueuePage />);
    const item = await screen.findByTestId('queue-item');
    expect(within(item).getByTestId('queue-awaiting').textContent).toBe(tr('moderation:awaiting.suspend_user'));
    expect(within(item).queryByTestId('queue-claim')).toBeNull();
    fireEvent.click(within(item).getByTestId('queue-confirm'));
    await waitFor(() => {
      expect(cosign).toHaveBeenCalledTimes(1);
    });
    expect(cosign.mock.calls[0]?.[0]).toMatchObject({ purpose: 'mod_action', target: 'p1' });
    expect(calls.some((c) => c.path === '/mod/items/p1/confirm')).toBe(false);
    fireEvent.click(within(item).getByTestId('queue-confirm'));
    await waitFor(() => {
      expect(calls.find((c) => c.path === '/mod/items/p1/confirm')?.body).toEqual({ intent: 'in1', assertion: { id: 'cred' } });
    });
  });

  it('decides actions and quarantine from the item', () => {
    expect(actionsFor(modItem({ summary: { kind_person: 1 } }))).toEqual(['suspend_user', 'dismiss']);
    expect(actionsFor(modItem({ source: 'csam', summary: { csam_match: 1, preview: 1 } }))).toEqual([
      'remove_post',
      'suspend_user',
      'dismiss',
    ]);
    expect(canQuarantine(modItem({ summary: { subtree_confirmed: 2, quarantine_k: 3 } }))).toBe(false);
    expect(canQuarantine(modItem({ summary: { subtree_confirmed: 3, quarantine_k: 3 } }))).toBe(true);
    expect(canQuarantine(modItem({}))).toBe(false);
    expect(reportRefOK('CT-1')).toBe(true);
    expect(reportRefOK('-x')).toBe(false);
    expect(reportRefOK('a'.repeat(129))).toBe(false);
  });
});

describe('moderation log', () => {
  it('names people in the horizon and shows pseudonyms for everyone else', async () => {
    const items: LogItem[] = [
      { day: '2026-10-16', action: 'remove_post', reason: 'spam', count: 2, subject: { ref: 'bobRef', handle: 'bob' } },
      { day: '2026-10-15', action: 'suspend_user', reason: 'bot', count: 1, subject: { pseudonym: 'a1b2c3d4e5f6' } },
    ];
    stub('member', (c) => (c.path === '/mod/log' ? json(200, { items }) : undefined));
    renderAt('/mod/log', <ModLogPage />);
    const rows = await screen.findAllByTestId('log-row');
    expect(rows).toHaveLength(2);
    const [named, pseudo] = rows as [HTMLElement, HTMLElement];
    expect(within(named).getByTestId('log-person').getAttribute('href')).toBe('/people/bobRef');
    expect(within(named).getByText(tr('moderation:action.remove_post'))).toBeTruthy();
    expect(within(pseudo).queryByTestId('log-person')).toBeNull();
    expect(within(pseudo).getByTestId('log-pseudonym').textContent).toBe(tr('moderation:log.pseudonym', { id: 'a1b2c3d4e5f6' }));
  });
});

describe('footer', () => {
  function renderFooter() {
    const router = createMemoryRouter([{ path: '/', element: <ModerationFooter /> }]);
    render(
      <QueryClientProvider client={makeQueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
  }
  it('links the terms for everyone and moderator pages for moderators', async () => {
    stub(undefined, () => undefined);
    renderFooter();
    const footer = await screen.findByTestId('site-footer');
    await waitFor(() => {
      expect([...footer.querySelectorAll('a')].map((a) => a.getAttribute('href'))).toEqual(['/terms']);
    });
  });
  it('adds the log, the blame inbox and the queue', async () => {
    stub('moderator', () => undefined);
    renderFooter();
    const footer = await screen.findByTestId('site-footer');
    await waitFor(() => {
      expect([...footer.querySelectorAll('a')].map((a) => a.getAttribute('href'))).toEqual([
        '/terms',
        '/mod/log',
        '/blame',
        '/mod',
        '/mod/recovery',
      ]);
    });
  });
});

describe('catalogs only', () => {
  afterAll(async () => {
    await act(() => i18n.changeLanguage('en'));
  });
  it('renders every visible string of the report page and the queue from the catalogs (pseudo-locale)', async () => {
    await act(async () => {
      await i18n.changeLanguage('en-XA');
      await i18n.loadNamespaces(['moderation', 'errors']);
    });
    stub('moderator', (c) =>
      c.path === '/mod/queue'
        ? json(200, {
            items: [
              modItem({
                subject: { ref: 's', handle: 'bob' },
                subject_inviter: { kind: 'user', handle: 'alice' },
                subject_probation: true,
                summary: { reports: 3, kind_person: 1 },
              }),
            ],
          })
        : undefined,
    );
    renderAt('/mod', <ModQueuePage />);
    await screen.findByTestId('queue-item');
    const texts: string[] = [];
    const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let n = walk.nextNode(); n; n = walk.nextNode()) {
      const s = n.textContent?.trim() ?? '';
      if (s && !['TIME', 'CODE'].includes(n.parentElement?.tagName ?? '') && !n.parentElement?.closest('[data-user-content]')) texts.push(s);
    }
    expect(texts.length).toBeGreaterThan(10);
    for (const s of texts) expect(s, s).toMatch(/^\[.*\]$/s);
  });
});
