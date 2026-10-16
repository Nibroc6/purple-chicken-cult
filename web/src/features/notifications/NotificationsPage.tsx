// Notifications page (§10.6). Owner: P0-W10 → P1-W05. Every kind renders from
// the catalogs with ICU plurals (who, how many, which reason code); people and
// posts are links only through the refs the server minted for the reader. The
// first visit with unread items marks them read up to the newest (they keep
// their "new" look for this visit); a live notification refreshes the list.
import { useInfiniteQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { errorKey } from '../../api/errors';
import { subscribe } from '../../api/sse';
import { Loading } from '../../app/Loading';
import { formatDateTime, formatRelative } from '../../lib/time';
import { fetchNotifications, markRead, setPostMute } from './api';
import { setUnread } from './unread';
import { messageArgs, type NotificationVM } from './vm';
import './notifications.css';

export const notificationsKey = ['notifications'] as const;

export default function NotificationsPage() {
  const { t } = useTranslation(['notifications', 'errors']);
  const qc = useQueryClient();
  const q = useInfiniteQuery({
    queryKey: notificationsKey,
    queryFn: ({ pageParam }) => fetchNotifications(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next,
    retry: false,
  });
  const items = q.data?.pages.flatMap((p) => p.items) ?? [];
  const first = q.data?.pages[0];
  const marked = useRef<string | undefined>(undefined);

  useEffect(() => {
    if (!first) return;
    setUnread(first.unread);
    const newest = first.items[0];
    if (!newest || first.unread === 0 || marked.current === newest.id) return;
    marked.current = newest.id;
    markRead(newest.id).then(
      () => {
        setUnread(0);
      },
      () => undefined, // stays unread; the next visit tries again
    );
  }, [first]);

  useEffect(
    () =>
      subscribe('notification', () => {
        void qc.invalidateQueries({ queryKey: notificationsKey });
      }),
    [qc],
  );

  return (
    <section className="page notifications-page" data-testid="notifications-page">
      <h1>{t('page.title')}</h1>
      {q.isPending ? <Loading /> : null}
      {q.error ? (
        <p className="notifications-error" role="alert">
          {t(errorKey(q.error) as 'errors:unknown')}
        </p>
      ) : null}
      {q.isSuccess && items.length === 0 ? (
        <p className="muted" data-testid="notifications-empty">
          {t('page.empty')}
        </p>
      ) : null}
      <ol className="notifications-list" data-testid="notifications-list">
        {items.map((n) => (
          <NotificationItem key={n.id} n={n} />
        ))}
      </ol>
      {q.hasNextPage ? (
        <button
          type="button"
          data-testid="notifications-more"
          disabled={q.isFetchingNextPage}
          onClick={() => {
            void q.fetchNextPage();
          }}
        >
          {t('page.more')}
        </button>
      ) : null}
    </section>
  );
}

function NotificationItem({ n }: { n: NotificationVM }) {
  const { t, i18n } = useTranslation('notifications');
  const args = messageArgs(n);
  return (
    <li className={`notification${n.read ? '' : ' notification-new'}`} data-testid="notification" data-kind={n.kind} data-read={n.read}>
      <p className="notification-text">
        {n.person ? (
          <>
            <Link to={`/people/${n.person.ref}`} className="notification-who" data-testid="notification-person">
              {t('item.handle', { handle: n.person.handle })}
            </Link>{' '}
          </>
        ) : null}
        {/* i18n-dynamic: notifications:kind. */}
        <span>{t(`kind.${n.kind}`, args)}</span>
      </p>
      <p className="notification-meta muted">
        {n.read ? null : <span className="notification-dot">{t('item.new')} </span>}
        <time dateTime={n.at.toISOString()} title={formatDateTime(n.at, i18n.language)}>
          {formatRelative(n.at, i18n.language)}
        </time>
        {n.post ? (
          <>
            {' '}
            <Link to={`/p/${n.post}`} data-testid="notification-post">
              {t('item.openPost')}
            </Link>
          </>
        ) : null}
        {n.kind === 'post_referenced' && n.post ? <MuteButton post={n.post} /> : null}
      </p>
    </li>
  );
}

function MuteButton({ post }: { post: string }) {
  const { t } = useTranslation(['notifications', 'errors']);
  const [muted, setMuted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<unknown>();
  const toggle = () => {
    setBusy(true);
    setErr(undefined);
    setPostMute(post, !muted).then(
      () => {
        setMuted(!muted);
        setBusy(false);
      },
      (e: unknown) => {
        setErr(e);
        setBusy(false);
      },
    );
  };
  return (
    <>
      {' '}
      <button type="button" className="notification-mute" data-testid="notification-mute" data-muted={muted} disabled={busy} onClick={toggle}>
        {muted ? t('mute.undo') : t('mute.action')}
      </button>
      {muted ? <span className="muted"> {t('mute.done')}</span> : null}
      {err ? (
        <span role="alert"> {t(errorKey(err) as 'errors:unknown')}</span>
      ) : null}
    </>
  );
}
