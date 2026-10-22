// The conversations list: group invitations first (join or decline), then
// every conversation, most recent first, with its last message and unread
// count. A live message refreshes it. Owner: direct messages (contract
// request dm-1).
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate } from 'react-router';
import { subscribe } from '../../api/sse';
import { Loading } from '../../app/Loading';
import { formatRelative } from '../../lib/time';
import { conversationName, fetchConversations, join, leave, listKey, messagesKey, type ConversationVM } from './api';
import { ErrorText } from './shared';
import type { LiveMessage } from './unread';
import './messages.css';

export default function MessagesPage() {
  const { t } = useTranslation('messages');
  const qc = useQueryClient();
  const q = useInfiniteQuery({
    queryKey: listKey,
    queryFn: ({ pageParam }) => fetchConversations(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next,
    retry: false,
  });
  useEffect(
    () =>
      subscribe<LiveMessage>('message', () => {
        void qc.invalidateQueries({ queryKey: listKey });
      }),
    [qc],
  );
  const items = q.data?.pages.flatMap((p) => p.items) ?? [];
  const invited = items.filter((c) => c.state === 'invited');
  const convs = items.filter((c) => c.state === 'member');
  return (
    <section className="page messages-page">
      <header className="messages-head">
        <h1>{t('title')}</h1>
        <Link to="/messages/new" className="messages-new" data-testid="messages-new">
          {t('new')}
        </Link>
      </header>
      <p className="muted">{t('intro')}</p>
      {q.isPending ? (
        <Loading />
      ) : q.error ? (
        <ErrorText err={q.error} />
      ) : (
        <>
          {invited.length > 0 ? (
            <section aria-labelledby="messages-invitations">
              <h2 id="messages-invitations">{t('invitations')}</h2>
              <ul className="messages-list" data-testid="messages-invitations">
                {invited.map((c) => (
                  <Invitation key={c.ref} conv={c} />
                ))}
              </ul>
            </section>
          ) : null}
          {convs.length === 0 && invited.length === 0 ? (
            <p data-testid="messages-empty">{t('empty')}</p>
          ) : (
            <ul className="messages-list" data-testid="messages-list" aria-label={t('conversations')}>
              {convs.map((c) => (
                <Row key={c.ref} conv={c} />
              ))}
            </ul>
          )}
          {q.hasNextPage ? (
            <button
              type="button"
              disabled={q.isFetchingNextPage}
              onClick={() => {
                void q.fetchNextPage();
              }}
            >
              {t('more')}
            </button>
          ) : null}
        </>
      )}
    </section>
  );
}

// i18n-dynamic: messages:event.
function previewText(c: ConversationVM, t: ReturnType<typeof useTranslation<'messages'>>['t']): string {
  const l = c.last;
  if (!l) return t('list.nothing');
  if (l.kind === 'deleted') return t('list.deleted');
  if (l.kind === 'event') {
    const who = l.from ? `@${l.from}` : t('event.someone');
    return l.fromMe ? t(`event.${l.event ?? 'joined'}Me` as 'event.joinedMe') : t(`event.${l.event ?? 'joined'}` as 'event.joined', { who });
  }
  const text = l.kind === 'post' ? t('list.post') : l.excerpt;
  if (l.fromMe) return t('list.you', { text });
  if (c.kind === 'group' && l.from) return t('list.from', { handle: l.from, text });
  return text;
}

function Row({ conv }: { conv: ConversationVM }) {
  const { t, i18n } = useTranslation('messages');
  const name = conversationName(conv, t);
  return (
    <li className="messages-row" data-testid="conversation-row" data-unread={conv.unread} data-kind={conv.kind}>
      <Link to={`/messages/${conv.ref}`} className="messages-row-link">
        <span className="messages-row-top">
          <span className="messages-row-name" data-user-content="">
            {name}
          </span>
          <time className="muted" dateTime={(conv.last?.at ?? conv.updatedAt).toISOString()}>
            {formatRelative(conv.last?.at ?? conv.updatedAt, i18n.language)}
          </time>
        </span>
        <span className="messages-row-bottom">
          <span className={conv.unread > 0 ? 'messages-row-preview messages-row-preview-unread' : 'messages-row-preview muted'} data-user-content="">
            {previewText(conv, t)}
          </span>
          {conv.muted ? <span className="muted messages-row-muted">{t('list.muted')}</span> : null}
          {conv.unread > 0 ? (
            <span className="nav-badge" data-testid="conversation-unread" aria-label={t('badge.label', { count: conv.unread })}>
              {conv.unread >= 100 ? t('badge.many') : t('badge.count', { count: conv.unread })}
            </span>
          ) : null}
        </span>
      </Link>
    </li>
  );
}

function Invitation({ conv }: { conv: ConversationVM }) {
  const { t } = useTranslation('messages');
  const qc = useQueryClient();
  const navigate = useNavigate();
  const accept = useMutation({
    mutationFn: () => join(conv.ref),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: messagesKey });
      void navigate(`/messages/${conv.ref}`);
    },
  });
  const decline = useMutation({
    mutationFn: () => leave(conv.ref),
    onSuccess: () => qc.invalidateQueries({ queryKey: messagesKey }),
  });
  const name = conv.title || t('name.untitled', { count: conv.memberCount });
  return (
    <li className="messages-row messages-invitation" data-testid="invitation-row">
      <p>
        {conv.invitedBy ? t('invite.from', { handle: conv.invitedBy.handle, name }) : name}{' '}
        <span className="muted">{t('invite.members', { count: conv.memberCount })}</span>
      </p>
      <p className="muted">{t('invite.explain')}</p>
      <div className="messages-actions">
        <button
          type="button"
          data-testid="invitation-join"
          disabled={accept.isPending || decline.isPending}
          onClick={() => {
            accept.mutate();
          }}
        >
          {t('invite.join')}
        </button>
        <button
          type="button"
          data-testid="invitation-decline"
          disabled={accept.isPending || decline.isPending}
          onClick={() => {
            decline.mutate();
          }}
        >
          {t('invite.decline')}
        </button>
      </div>
      <ErrorText err={accept.error ?? decline.error} />
    </li>
  );
}
