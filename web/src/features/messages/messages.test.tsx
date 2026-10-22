// Direct messages: view-model mapping (a shared post the viewer cannot see is
// only a placeholder), the unread badge (read on load, live over SSE), the
// list with invitations, a conversation (oldest first, marked read, Enter
// sends, delete own, report others) and the share sheet sending one post into
// several conversations.
import { QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { components } from '../../api/schema.gen';
import { dispatch, resetSSE } from '../../api/sse';
import { makeQueryClient } from '../../app/App';
import { i18n } from '../../i18n/setup';
import { asRef } from '../../vm/types';
import { postJSON } from '../post/testdata';
import { conversationName, toConversationVM, toMessageVM } from './api';
import ConversationPage from './ConversationPage';
import MessagesPage from './MessagesPage';
import { ShareModal } from './ShareModal';
import { MessagesBadge } from './slots';
import { resetUnread } from './unread';

type S = components['schemas'];

const tr = i18n.t.bind(i18n) as unknown as (k: string, o?: Record<string, unknown>) => string;

class FakeES {
  addEventListener() {
    return undefined;
  }
  close() {
    return undefined;
  }
}

interface Call {
  method: string;
  path: string;
  query: Record<string, string>;
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
      const u = new URL(req.url);
      const c = {
        method: req.method,
        path: u.pathname.replace(/^\/api/, ''),
        query: Object.fromEntries(u.searchParams),
        body: text ? (JSON.parse(text) as unknown) : undefined,
      };
      calls.push(c);
      return handler(c) ?? json(404, { error: 'not_found' });
    }),
  );
  return calls;
}

beforeEach(() => {
  vi.stubGlobal('EventSource', FakeES);
  resetSSE();
  resetUnread();
});

afterEach(() => {
  resetSSE();
  vi.unstubAllGlobals();
});

const at = '2026-01-01T00:00:00Z';
const bob: S['ConvMember'] = { ref: 'r-bob', handle: 'bob', display_name: 'Bob', relation: 'friend' };
const carol: S['ConvMember'] = { ref: 'r-carol', handle: 'carol', display_name: 'Carol', relation: 'friend_plus' };

const conv = (over: Partial<S['ConversationView']> = {}): S['ConversationView'] => ({
  ref: 'c1',
  kind: 'direct',
  state: 'member',
  members: [bob],
  member_count: 2,
  can_send: true,
  unread: 0,
  updated_at: at,
  ...over,
});

const msg = (ref: string, over: Partial<S['MessageView']> = {}): S['MessageView'] => ({
  ref,
  kind: 'text',
  mine: false,
  from: bob,
  body: `message ${ref}`,
  at,
  ...over,
});

const post = postJSON({ ref: 'p1', body: 'the shared post' }) as S['PostView'];

function wrap(ui: React.ReactNode, path: string) {
  function Where() {
    const l = useLocation();
    return <output data-testid="where">{l.pathname + l.search}</output>;
  }
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/messages" element={ui} />
          <Route path="/messages/:ref" element={ui} />
          <Route path="/p/:ref" element={ui} />
          <Route path="/messages/new" element={<p>new</p>} />
        </Routes>
        <Where />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('view-models', () => {
  it('maps a shared post the viewer cannot see to a placeholder only', () => {
    const vm = toMessageVM(msg('m1', { body: '', post: { unavailable: true } }));
    expect(vm.post).toBe('unavailable');
    expect(vm.body).toBeNull();
    const seen = toMessageVM(msg('m2', { post: { post } }));
    expect(seen.post).not.toBe('unavailable');
    expect(seen.body?.source).toBe('message m2');
  });

  it('names a conversation by its title, else its members', () => {
    const t = (k: 'name.untitled', o: { count: number }) => tr(`messages:${k}`, o);
    expect(conversationName(toConversationVM(conv()), t)).toBe('@bob');
    expect(conversationName(toConversationVM(conv({ kind: 'group', title: 'plans', members: [bob, carol] })), t)).toBe('plans');
    expect(conversationName(toConversationVM(conv({ kind: 'group', members: [bob, carol] })), t)).toBe('@bob, @carol');
    expect(conversationName(toConversationVM(conv({ kind: 'group', members: [], member_count: 3 })), t)).toBe(
      tr('messages:name.untitled', { count: 3 }),
    );
  });
});

describe('MessagesBadge', () => {
  it('reads the count, then follows live message events', async () => {
    stubAPI((c) => (c.path === '/messages/unread' ? json(200, { unread: 2 }) : undefined));
    render(<MessagesBadge />);
    await waitFor(() => {
      expect(screen.getByTestId('messages-badge').dataset.count).toBe('2');
    });
    act(() => {
      dispatch('message', { conversation: 'c1', unread: 120 });
    });
    expect(screen.getByTestId('messages-badge').textContent).toBe(tr('messages:badge.many'));
    act(() => {
      dispatch('message', { conversation: 'c1', unread: 0 });
    });
    expect(screen.queryByTestId('messages-badge')).toBeNull();
  });
});

describe('MessagesPage', () => {
  it('lists invitations first, then conversations with their unread count', async () => {
    const calls = stubAPI((c) => {
      if (c.path === '/messages' && c.method === 'GET') {
        return json(200, {
          items: [
            conv({ unread: 3, last: { kind: 'text', excerpt: 'hello there', from_me: false, from: 'bob', at } }),
            conv({ ref: 'g1', kind: 'group', state: 'invited', title: 'plans', members: [], member_count: 4, invited_by: carol }),
          ],
        });
      }
      if (c.path === '/messages/g1/join') return json(200, conv({ ref: 'g1', kind: 'group' }));
      return undefined;
    });
    wrap(<MessagesPage />, '/messages');
    const row = await screen.findByTestId('conversation-row');
    expect(row.dataset.unread).toBe('3');
    expect(within(row).getByText('hello there')).toBeTruthy();
    const inv = screen.getByTestId('invitation-row');
    expect(inv.textContent).toContain(tr('messages:invite.from', { handle: 'carol', name: 'plans' }));
    fireEvent.click(within(inv).getByTestId('invitation-join'));
    await waitFor(() => {
      expect(screen.getByTestId('where').textContent).toBe('/messages/g1');
    });
    expect(calls.some((c) => c.method === 'POST' && c.path === '/messages/g1/join')).toBe(true);
  });
});

describe('ConversationPage', () => {
  it('shows the thread oldest first, marks it read, sends on Enter and hides an unseeable post', async () => {
    let sent = false;
    const calls = stubAPI((c) => {
      if (c.path === '/messages/c1' && c.method === 'GET') return json(200, conv({ unread: 2 }));
      if (c.path === '/messages/c1/messages' && c.method === 'GET') {
        const items = [
          msg('m3', { body: '', post: { unavailable: true } }),
          msg('m2', { post: { post } }),
          msg('m1', { mine: true, from: undefined, body: 'mine' }),
        ];
        return json(200, { items: sent ? [msg('m4', { mine: true, from: undefined, body: 'hi bob' }), ...items] : items });
      }
      if (c.path === '/messages/c1/read') return json(204, null);
      if (c.path === '/messages/unread') return json(200, { unread: 0 });
      if (c.path === '/messages/c1/messages' && c.method === 'POST') {
        sent = true;
        return json(201, msg('m4', { mine: true, from: undefined, body: 'hi bob' }));
      }
      if (c.path === '/messages/c1/messages/m1' && c.method === 'DELETE') return json(204, null);
      return undefined;
    });
    wrap(<ConversationPage />, '/messages/c1');
    await screen.findByTestId('conversation-thread');
    const bodies = screen.getAllByTestId('message').map((m) => m.dataset.mine);
    expect(bodies).toEqual(['true', 'false', 'false']);
    expect(screen.getByTestId('message-post').textContent).toContain('the shared post');
    expect(screen.getByTestId('message-post-unavailable').textContent).toBe(tr('messages:post.unavailable'));
    await waitFor(() => {
      expect(calls.find((c) => c.path === '/messages/c1/read')?.body).toEqual({ up_to: 'm3' });
    });

    const box = screen.getByTestId('composer-body');
    fireEvent.change(box, { target: { value: 'hi bob' } });
    fireEvent.keyDown(box, { key: 'Enter', shiftKey: true });
    expect(calls.some((c) => c.method === 'POST' && c.path === '/messages/c1/messages')).toBe(false);
    fireEvent.keyDown(box, { key: 'Enter' });
    await waitFor(() => {
      expect(screen.getAllByTestId('message')).toHaveLength(4);
    });
    expect(calls.find((c) => c.method === 'POST' && c.path === '/messages/c1/messages')?.body).toEqual({ body: 'hi bob' });
    expect((box as HTMLTextAreaElement).value).toBe('');

    // Only others' messages can be reported; only one's own deleted (after a confirm).
    const mine = screen.getAllByTestId('message').filter((m) => m.dataset.mine === 'true');
    expect(within(mine[0] ?? document.body).queryByTestId('message-report')).toBeNull();
    const others = screen.getAllByTestId('message').filter((m) => m.dataset.mine === 'false');
    expect(within(others[0] ?? document.body).queryByTestId('message-delete')).toBeNull();
    const first = mine.find((m) => m.textContent.includes('mine'));
    if (!first) throw new Error('no own message');
    fireEvent.click(within(first).getByTestId('message-delete'));
    fireEvent.click(within(first).getByTestId('message-delete-yes'));
    await waitFor(() => {
      expect(calls.some((c) => c.method === 'DELETE' && c.path === '/messages/c1/messages/m1')).toBe(true);
    });
    fireEvent.click(within(others[0] ?? document.body).getByTestId('message-report'));
    expect(screen.getByTestId('where').textContent).toContain('report=message');
  });

  it('says why a direct conversation can no longer send', async () => {
    stubAPI((c) => {
      if (c.path === '/messages/c1' && c.method === 'GET') return json(200, conv({ can_send: false }));
      if (c.path === '/messages/c1/messages') return json(200, { items: [] });
      return undefined;
    });
    wrap(<ConversationPage />, '/messages/c1');
    expect((await screen.findByTestId('conversation-cant-send')).textContent).toBe(tr('messages:conv.cantSend'));
    expect(screen.queryByTestId('conversation-composer')).toBeNull();
  });
});

describe('ShareModal', () => {
  it('sends the post into each chosen conversation with the note', async () => {
    const calls = stubAPI((c) => {
      if (c.path === '/posts/p1') return json(200, post);
      if (c.path === '/messages' && c.method === 'GET') {
        return json(200, {
          items: [conv(), conv({ ref: 'g1', kind: 'group', title: 'plans', members: [bob, carol] }), conv({ ref: 'c2', can_send: false })],
        });
      }
      if (c.method === 'POST' && /^\/messages\/(c1|g1)\/messages$/.test(c.path)) return json(201, msg('mx', { mine: true }));
      return undefined;
    });
    wrap(<ShareModal />, `/p/p1?share=${asRef('p1')}`);
    await screen.findByTestId('share-modal');
    const targets = await screen.findAllByTestId('share-target');
    // A conversation that cannot send is not offered.
    expect(targets.map((t) => t.textContent)).toEqual(['@bob', 'plans']);
    expect(screen.getByTestId('share-new').getAttribute('href')).toBe('/messages/new?post=p1');
    targets.forEach((t) => {
      fireEvent.click(within(t).getByRole('checkbox'));
    });
    fireEvent.change(screen.getByTestId('share-note'), { target: { value: 'look' } });
    fireEvent.click(screen.getByTestId('share-send'));
    await screen.findByTestId('share-done');
    const sends = calls.filter((c) => c.method === 'POST');
    expect(sends.map((c) => c.path).sort()).toEqual(['/messages/c1/messages', '/messages/g1/messages']);
    expect(sends[0]?.body).toEqual({ body: 'look', post: 'p1' });
  });
});
