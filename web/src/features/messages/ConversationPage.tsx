// One conversation: its messages oldest to newest (older pages load on
// request), the composer, and the conversation's options (members, mute,
// rename, add people, leave). Opening it marks it read up to the newest
// message; a live message for it refreshes the thread. An invitation shows
// only who invited the viewer, with Join and Decline. Owner: direct messages
// (contract request dm-1).
import { useInfiniteQuery, useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate, useParams } from 'react-router';
import { ApiError } from '../../api/errors';
import { subscribe } from '../../api/sse';
import { Loading } from '../../app/Loading';
import { NotFound } from '../../app/NotFound';
import { IconButton } from '../../lib/IconButton';
import { formatDateTime } from '../../lib/time';
import { Markdown } from '../../markdown';
import { asRef, type Ref } from '../../vm/types';
import { useOpenReport } from '../moderation/ReportModal';
import { codePoints } from '../post/vm';
import {
  convKey,
  conversationName,
  deleteMessage,
  fetchConversation,
  fetchMessages,
  invite,
  join,
  leave,
  listKey,
  markRead,
  MaxBody,
  MaxMembers,
  MaxTitle,
  messagesKey,
  removeMember,
  rename,
  sendMessage,
  setMuted,
  threadKey,
  type ConversationVM,
  type MessageVM,
} from './api';
import { Confirm, ErrorText, PeoplePicker, SharedPost } from './shared';
import { refreshUnread, type LiveMessage } from './unread';
import './messages.css';

export default function ConversationPage() {
  const { t } = useTranslation('messages');
  const ref = asRef(useParams().ref ?? '');
  const qc = useQueryClient();
  const q = useQuery({ queryKey: convKey(ref), queryFn: () => fetchConversation(ref), retry: false });
  useEffect(
    () =>
      subscribe<LiveMessage>('message', (d) => {
        if (d.conversation !== ref) return;
        void qc.invalidateQueries({ queryKey: convKey(ref) });
        void qc.invalidateQueries({ queryKey: threadKey(ref) });
      }),
    [qc, ref],
  );
  if (q.error instanceof ApiError && q.error.isNotFound) return <NotFound />;
  return (
    <section className="page messages-page conversation-page" data-testid="conversation-page">
      <p className="conversation-back">
        <Link to="/messages">{t('conv.back')}</Link>
      </p>
      {q.isPending ? (
        <>
          <h1>{t('title')}</h1>
          <Loading />
        </>
      ) : q.error ? (
        <>
          <h1>{t('title')}</h1>
          <ErrorText err={q.error} />
        </>
      ) : q.data.state === 'invited' ? (
        <InvitedView conv={q.data} />
      ) : (
        <Joined key={q.data.ref} conv={q.data} />
      )}
    </section>
  );
}

function InvitedView({ conv }: { conv: ConversationVM }) {
  const { t } = useTranslation('messages');
  const qc = useQueryClient();
  const navigate = useNavigate();
  const accept = useMutation({ mutationFn: () => join(conv.ref), onSuccess: () => qc.invalidateQueries({ queryKey: messagesKey }) });
  const decline = useMutation({
    mutationFn: () => leave(conv.ref),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: messagesKey });
      void navigate('/messages', { replace: true });
    },
  });
  const name = conv.title || t('name.untitled', { count: conv.memberCount });
  return (
    <>
      <h1 data-user-content="">{name}</h1>
      <p data-testid="conversation-invited">
        {conv.invitedBy ? t('invite.from', { handle: conv.invitedBy.handle, name }) : null}{' '}
        <span className="muted">{t('invite.members', { count: conv.memberCount })}</span>
      </p>
      <p className="muted">{t('invite.explain')}</p>
      <div className="messages-actions">
        <button
          type="button"
          data-testid="invitation-join"
          disabled={accept.isPending}
          onClick={() => {
            accept.mutate();
          }}
        >
          {t('invite.join')}
        </button>
        <button
          type="button"
          data-testid="invitation-decline"
          disabled={decline.isPending}
          onClick={() => {
            decline.mutate();
          }}
        >
          {t('invite.decline')}
        </button>
      </div>
      <ErrorText err={accept.error ?? decline.error} />
    </>
  );
}

// refreshAll re-reads the conversation, its thread and the list.
function refreshAll(qc: QueryClient, ref: Ref): void {
  void qc.invalidateQueries({ queryKey: convKey(ref) });
  void qc.invalidateQueries({ queryKey: threadKey(ref) });
  void qc.invalidateQueries({ queryKey: listKey });
}

function Joined({ conv }: { conv: ConversationVM }) {
  const { t } = useTranslation('messages');
  const qc = useQueryClient();
  const thread = useInfiniteQuery({
    queryKey: threadKey(conv.ref),
    queryFn: ({ pageParam }) => fetchMessages(conv.ref, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next,
    retry: false,
  });
  // Pages come newest first; the thread reads top to bottom, oldest first.
  const items = (thread.data?.pages.flatMap((p) => p.items) ?? []).slice().reverse();
  const newest = thread.data?.pages[0]?.items[0];

  const marked = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (!newest || conv.unread === 0 || marked.current === newest.ref) return;
    marked.current = newest.ref;
    markRead(conv.ref, newest.ref).then(
      () => {
        qc.setQueryData<ConversationVM>(convKey(conv.ref), (c) => (c ? { ...c, unread: 0 } : c));
        void qc.invalidateQueries({ queryKey: listKey });
        refreshUnread();
      },
      () => undefined, // stays unread; the next visit tries again
    );
  }, [newest, conv.unread, conv.ref, qc]);

  // Keep the newest message in view when one arrives (not when older ones load).
  const end = useRef<HTMLDivElement>(null);
  const lastSeen = useRef<string | undefined>(undefined);
  useLayoutEffect(() => {
    if (!newest || lastSeen.current === newest.ref) return;
    lastSeen.current = newest.ref;
    const el = end.current;
    // jsdom has no scrollIntoView.
    if (el && typeof el.scrollIntoView === 'function') el.scrollIntoView({ block: 'end' });
  }, [newest]);

  const name = conversationName(conv, t);
  return (
    <>
      <header className="conversation-head">
        <h1 data-user-content="" data-testid="conversation-name">
          {name}
        </h1>
        <Options conv={conv} />
      </header>
      {conv.muted ? <p className="muted">{t('conv.mutedHint')}</p> : null}
      {thread.isPending ? (
        <Loading />
      ) : thread.error ? (
        <ErrorText err={thread.error} />
      ) : (
        <div className="conversation-thread" data-testid="conversation-thread">
          {thread.hasNextPage ? (
            <button
              type="button"
              className="conversation-older"
              data-testid="conversation-older"
              disabled={thread.isFetchingNextPage}
              onClick={() => {
                void thread.fetchNextPage();
              }}
            >
              {t('conv.older')}
            </button>
          ) : (
            <p className="muted conversation-beginning">{t('conv.beginning')}</p>
          )}
          <ol className="conversation-messages" aria-label={name}>
            {items.map((m) => (
              <MessageItem key={m.ref} conv={conv} msg={m} />
            ))}
          </ol>
          <div ref={end} />
        </div>
      )}
      {conv.canSend ? (
        <Composer conv={conv} />
      ) : (
        <p className="muted" data-testid="conversation-cant-send">
          {t('conv.cantSend')}
        </p>
      )}
    </>
  );
}

// i18n-dynamic: messages:event.
function MessageItem({ conv, msg }: { conv: ConversationVM; msg: MessageVM }) {
  const { t, i18n } = useTranslation('messages');
  const qc = useQueryClient();
  const openReport = useOpenReport();
  const del = useMutation({ mutationFn: () => deleteMessage(conv.ref, msg.ref), onSuccess: () => { refreshAll(qc, conv.ref); } });
  const when = <time dateTime={msg.at.toISOString()}>{formatDateTime(msg.at, i18n.language)}</time>;
  if (msg.kind === 'event') {
    const ev = msg.event ?? 'joined';
    // The server leaves out the subject when it is the viewer; someone
    // removed no longer reads the group, so a removal is never about them.
    const me = !msg.subject && msg.mine && ev !== 'removed';
    const handle = msg.subject?.handle ?? msg.from?.handle;
    return (
      <li className="message message-event muted" data-testid="message-event" data-event={ev}>
        {me ? t(`event.${ev}Me` as 'event.joinedMe') : t(`event.${ev}` as 'event.joined', { who: handle ? `@${handle}` : t('event.someone') })} · {when}
      </li>
    );
  }
  const who = msg.mine ? t('conv.you') : msg.from ? `@${msg.from.handle}` : t('event.someone');
  return (
    <li className={msg.mine ? 'message message-mine' : 'message'} data-testid="message" data-kind={msg.kind} data-mine={msg.mine}>
      <div className="message-bubble">
        {conv.kind === 'group' || !msg.mine ? <p className="message-from">{who}</p> : null}
        {msg.kind === 'deleted' ? (
          <p className="muted message-deleted" data-testid="message-deleted">
            {t('conv.deleted')}
          </p>
        ) : (
          <>
            {msg.body ? (
              <div className="message-body" data-testid="message-body" data-user-content="">
                <Markdown doc={msg.body} />
              </div>
            ) : null}
            {msg.post ? <SharedPost post={msg.post} /> : null}
          </>
        )}
        <p className="message-meta muted">
          {when}
          {msg.kind !== 'deleted' && msg.mine ? (
            <Confirm
              className="messages-link-button"
              label={t('conv.deleteAction')}
              question={t('conv.deleteConfirm')}
              testId="message-delete"
              disabled={del.isPending}
              onConfirm={() => {
                del.mutate();
              }}
            />
          ) : null}
          {msg.kind !== 'deleted' && !msg.mine ? (
            <IconButton
              icon="report"
              label={t('conv.reportAction')}
              data-testid="message-report"
              onClick={() => {
                openReport({ kind: 'message', ref: msg.ref });
              }}
            />
          ) : null}
        </p>
        <ErrorText err={del.error} />
      </div>
    </li>
  );
}

function Composer({ conv }: { conv: ConversationVM }) {
  const { t } = useTranslation('messages');
  const qc = useQueryClient();
  const id = useId();
  const hintId = useId();
  const [body, setBody] = useState('');
  const send = useMutation({
    mutationFn: (text: string) => sendMessage(conv.ref, text),
    onSuccess: () => {
      setBody('');
      refreshAll(qc, conv.ref);
    },
  });
  const left = MaxBody - codePoints(body);
  const submit = () => {
    const text = body.trim();
    if (!text || left < 0 || send.isPending) return;
    send.mutate(text);
  };
  return (
    <form
      className="conversation-composer"
      data-testid="conversation-composer"
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
    >
      <label htmlFor={id} className="visually-hidden">
        {t('composer.label')}
      </label>
      <textarea
        id={id}
        name="body"
        rows={2}
        data-testid="composer-body"
        placeholder={t('composer.placeholder')}
        aria-describedby={hintId}
        value={body}
        onChange={(e) => {
          setBody(e.target.value);
        }}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
            e.preventDefault();
            submit();
          }
        }}
      />
      <IconButton icon="send" label={t('composer.send')} type="submit" data-testid="composer-send" disabled={!body.trim() || left < 0 || send.isPending} />
      <p id={hintId} className="muted conversation-composer-hint">
        {left < 200 ? t('composer.counter', { count: left }) : t('composer.hint')}
      </p>
      <ErrorText err={send.error} />
    </form>
  );
}

function Options({ conv }: { conv: ConversationVM }) {
  const { t } = useTranslation('messages');
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [renaming, setRenaming] = useState(false);
  const [adding, setAdding] = useState(false);
  const mute = useMutation({ mutationFn: () => setMuted(conv.ref, !conv.muted), onSuccess: () => { refreshAll(qc, conv.ref); } });
  const out = useMutation({
    mutationFn: () => leave(conv.ref),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: messagesKey });
      refreshUnread();
      void navigate('/messages', { replace: true });
    },
  });
  const remove = useMutation({ mutationFn: (who: Ref) => removeMember(conv.ref, who), onSuccess: () => { refreshAll(qc, conv.ref); } });
  const group = conv.kind === 'group';
  return (
    <details className="conversation-options" data-testid="conversation-options">
      <summary>{t('conv.options')}</summary>
      <div className="conversation-options-body">
        <h2>{t('conv.membersCount', { count: conv.memberCount })}</h2>
        <ul className="conversation-members" data-testid="conversation-members">
          <li>{t('conv.you')}{conv.admin ? <span className="muted"> · {t('conv.admin')}</span> : null}</li>
          {conv.members.map((m) => (
            <li key={m.ref} data-testid="conversation-member" data-handle={m.handle}>
              <Link to={`/people/${m.ref}`}>
                {m.displayName ? <span data-user-content="">{m.displayName} </span> : null}
                <span className="muted">@{m.handle}</span>
              </Link>
              {m.admin ? <span className="muted"> · {t('conv.admin')}</span> : null}
              {group && conv.admin ? (
                <Confirm
                  label={t('conv.remove', { handle: m.handle })}
                  question={t('conv.removeConfirm', { handle: m.handle })}
                  testId="member-remove"
                  disabled={remove.isPending}
                  onConfirm={() => {
                    remove.mutate(m.ref);
                  }}
                />
              ) : null}
            </li>
          ))}
        </ul>
        <div className="messages-actions">
          <button
            type="button"
            data-testid="conversation-mute"
            aria-pressed={conv.muted}
            disabled={mute.isPending}
            onClick={() => {
              mute.mutate();
            }}
          >
            {conv.muted ? t('conv.unmute') : t('conv.mute')}
          </button>
          {group ? (
            <>
              <button
                type="button"
                data-testid="conversation-rename"
                onClick={() => {
                  setRenaming((r) => !r);
                }}
              >
                {t('conv.rename')}
              </button>
              {conv.memberCount < MaxMembers ? (
                <button
                  type="button"
                  data-testid="conversation-add"
                  aria-expanded={adding}
                  onClick={() => {
                    setAdding((a) => !a);
                  }}
                >
                  {t('conv.addPeople')}
                </button>
              ) : null}
            </>
          ) : null}
          <Confirm
            label={group ? t('conv.leave') : t('conv.clear')}
            question={group ? t('conv.leaveConfirm') : t('conv.clearConfirm')}
            testId="conversation-leave"
            disabled={out.isPending}
            onConfirm={() => {
              out.mutate();
            }}
          />
        </div>
        {renaming ? (
          <Rename
            conv={conv}
            onDone={() => {
              setRenaming(false);
            }}
          />
        ) : null}
        {adding ? (
          <AddPeople
            conv={conv}
            onDone={() => {
              setAdding(false);
            }}
          />
        ) : null}
        <ErrorText err={mute.error ?? out.error ?? remove.error} />
      </div>
    </details>
  );
}

function Rename({ conv, onDone }: { conv: ConversationVM; onDone: () => void }) {
  const { t } = useTranslation('messages');
  const qc = useQueryClient();
  const id = useId();
  const [title, setTitle] = useState(conv.title);
  const save = useMutation({
    mutationFn: () => rename(conv.ref, title.trim()),
    onSuccess: () => {
      refreshAll(qc, conv.ref);
      onDone();
    },
  });
  return (
    <form
      className="messages-field"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <label htmlFor={id}>{t('start.groupTitle')}</label>
      <input
        id={id}
        name="title"
        data-testid="rename-title"
        maxLength={MaxTitle}
        value={title}
        onChange={(e) => {
          setTitle(e.target.value);
        }}
      />
      <div className="messages-actions">
        <button type="submit" data-testid="rename-save" disabled={save.isPending}>
          {t('conv.renameSave')}
        </button>
        <button type="button" onClick={onDone}>
          {t('conv.renameCancel')}
        </button>
      </div>
      <ErrorText err={save.error} />
    </form>
  );
}

function AddPeople({ conv, onDone }: { conv: ConversationVM; onDone: () => void }) {
  const { t } = useTranslation('messages');
  const qc = useQueryClient();
  const [chosen, setChosen] = useState<Ref[]>([]);
  const add = useMutation({
    mutationFn: () => invite(conv.ref, chosen),
    onSuccess: () => {
      refreshAll(qc, conv.ref);
      onDone();
    },
  });
  const exclude = new Set(conv.members.map((m) => m.handle));
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (chosen.length > 0) add.mutate();
      }}
    >
      <PeoplePicker chosen={chosen} onChange={setChosen} exclude={exclude} max={MaxMembers - conv.memberCount} />
      <button type="submit" data-testid="add-submit" disabled={chosen.length === 0 || add.isPending}>
        {t('conv.addPeople')}
      </button>
      <ErrorText err={add.error} />
    </form>
  );
}
