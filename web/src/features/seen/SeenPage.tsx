// /seen: posts the viewer saw in the last 14 days, most recently seen first
// (§4.3 "recover posts you scrolled past quickly"). These cards are not
// seen-tracked again (that would reorder the list). Owner: P0-W06 → P1-W08.
import { useInfiniteQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { Loading } from '../../app/Loading';
import { errorKey } from '../../api/errors';
import { EngageBar } from '../engage/EngageBar';
import { usePostActions } from '../engage/usePostActions';
import { PostPreview } from '../post/shared';
import { fetchRecent, recentKey } from './api';
import './seen.css';

export default function SeenPage() {
  const { t } = useTranslation(['seen', 'errors']);
  const actions = usePostActions();
  const q = useInfiniteQuery({
    queryKey: recentKey,
    queryFn: ({ pageParam }) => fetchRecent(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next,
    retry: false,
  });
  const posts = q.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <section className="page seen-page">
      <h1>{t('page.title')}</h1>
      <p className="muted">{t('page.intro')}</p>
      {q.isPending ? <Loading /> : null}
      {q.error ? (
        <p className="seen-error" role="alert">
          {t(errorKey(q.error) as 'errors:unknown')}
        </p>
      ) : null}
      {q.isSuccess && posts.length === 0 ? (
        <p data-testid="seen-empty">{t('page.empty')}</p>
      ) : null}
      <ol className="seen-list" data-testid="seen-list">
        {posts.map((p) => (
          <li key={p.ref} className="seen-item" data-testid="seen-item" data-ref={p.ref}>
            <PostPreview post={p} />
            <EngageBar post={p} actions={actions} />
            <p className="post-links">
              <Link to={`/p/${p.ref}`}>{t('page.open')}</Link> <Link to={`/p/${p.ref}/likes`}>{t('page.likes')}</Link>
            </p>
          </li>
        ))}
      </ol>
      {q.hasNextPage ? (
        <button
          type="button"
          data-testid="seen-more"
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
