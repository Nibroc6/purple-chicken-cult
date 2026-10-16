// Blame inbox (/blame; §10.4). Owner: P0-W12 → P1-W06. After a moderator
// confirms an action against someone the reader invited, the reader sees what
// was done and why (a reason code from the catalog). The removed post is shown
// only if the reader could see it; who reported it is never shown. Opening the
// page marks the entries read (they keep their "new" look for this visit).
import { useInfiniteQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { errorKey } from '../../api/errors';
import { Loading } from '../../app/Loading';
import { formatDateTime, formatRelative } from '../../lib/time';
import { fetchBlame, type BlameItem } from './api';
import './moderation.css';

export const blameKey = ['moderation', 'blame'] as const;

export default function BlamePage() {
  const { t } = useTranslation(['moderation', 'errors']);
  const q = useInfiniteQuery({
    queryKey: blameKey,
    queryFn: ({ pageParam }) => fetchBlame(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next,
    retry: false,
  });
  const items = q.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <section className="page moderation-page" data-testid="blame-page">
      <h1>{t('blame.title')}</h1>
      <p className="muted">{t('blame.intro')}</p>
      {q.isPending ? <Loading /> : null}
      {q.error ? (
        <p className="moderation-error" role="alert">
          {t(errorKey(q.error) as 'errors:unknown')}
        </p>
      ) : null}
      {q.isSuccess && items.length === 0 ? (
        <p className="muted" data-testid="blame-empty">
          {t('blame.empty')}
        </p>
      ) : null}
      <ol className="moderation-list" data-testid="blame-list">
        {items.map((it) => (
          <BlameEntry key={it.id} it={it} />
        ))}
      </ol>
      {q.hasNextPage ? (
        <button
          type="button"
          disabled={q.isFetchingNextPage}
          onClick={() => {
            void q.fetchNextPage();
          }}
        >
          {t('blame.more')}
        </button>
      ) : null}
      <p>
        <Link to="/mod/log">{t('footer.log')}</Link>
      </p>
    </section>
  );
}

function BlameEntry({ it }: { it: BlameItem }) {
  const { t, i18n } = useTranslation('moderation');
  const at = new Date(it.at);
  return (
    <li className={`moderation-item${it.read ? '' : ' moderation-new'}`} data-testid="blame-item" data-action={it.action} data-read={it.read}>
      <p className="moderation-text">
        {t('blame.item', { action: it.action, handle: it.invitee_handle })}
      </p>
      <p className="moderation-meta muted">
        {it.read ? null : <span className="moderation-dot">{t('blame.new')} </span>}
        {/* i18n-dynamic: moderation:reason. */}
        <span data-testid="blame-reason">{t('blame.reason', { reason: t(`reason.${it.reason}` as 'reason.other') })}</span>{' '}
        <time dateTime={at.toISOString()} title={formatDateTime(at, i18n.language)}>
          {formatRelative(at, i18n.language)}
        </time>
      </p>
      {it.post ? (
        <figure className="moderation-post" data-testid="blame-post">
          <figcaption className="muted">{t('blame.post', { handle: it.post.author.handle })}</figcaption>
          <blockquote data-user-content="">{it.post.body}</blockquote>
        </figure>
      ) : null}
    </li>
  );
}
