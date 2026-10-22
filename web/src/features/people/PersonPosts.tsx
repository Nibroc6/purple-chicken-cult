// A person's post history on their page: every post of theirs the viewer can
// see, newest or oldest first, page by page. The server applies the feed's
// checks to each post (audience, distance, probation, blocks, hides), so the
// list never shows more than the feed would. Owner: P1-W03.
import { useInfiniteQuery } from '@tanstack/react-query';
import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { Loading } from '../../app/Loading';
import { ApiError } from '../../api/errors';
import { EngageBar } from '../engage/EngageBar';
import { usePostActions } from '../engage/usePostActions';
import { PostPreview } from '../post/shared';
import { fetchPersonPosts, type PostOrder } from './api';
import { ErrorText, peopleKey } from './shared';
import type { PeopleVM } from './vm';

const orders: PostOrder[] = ['newest', 'oldest'];

// i18n-dynamic: people:posts.order.
export function PersonPosts({ person }: { person: PeopleVM }) {
  const { t } = useTranslation('people');
  const titleId = useId();
  const actions = usePostActions();
  const [order, setOrder] = useState<PostOrder>('newest');
  const q = useInfiniteQuery({
    queryKey: [...peopleKey, 'posts', person.ref, order],
    queryFn: ({ pageParam }) => fetchPersonPosts(person.ref, order, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next,
    retry: false,
    refetchOnWindowFocus: false,
  });
  // Not found: the person dropped out of range since the page loaded.
  if (q.error instanceof ApiError && q.error.isNotFound) return null;
  const posts = q.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <section className="people-section people-posts" aria-labelledby={titleId} data-testid="person-posts">
      <div className="people-posts-head">
        <h2 id={titleId}>{t('posts.title')}</h2>
        <div className="people-posts-order" role="group" aria-label={t('posts.orderLabel')}>
          {orders.map((o) => (
            <button
              key={o}
              type="button"
              className="people-posts-order-btn"
              data-testid={`person-posts-${o}`}
              aria-pressed={order === o}
              onClick={() => {
                setOrder(o);
              }}
            >
              {t(`posts.order.${o}` as 'posts.order.newest')}
            </button>
          ))}
        </div>
      </div>
      {q.isPending ? <Loading /> : null}
      <ErrorText err={q.error} />
      {q.isSuccess && posts.length === 0 ? (
        <p className="muted" data-testid="person-posts-empty">
          {t('posts.empty', { handle: person.handle })}
        </p>
      ) : null}
      <ol className="people-posts-list" data-testid="person-posts-list">
        {posts.map((p) => (
          <li key={p.ref} className="people-posts-item" data-testid="person-post" data-ref={p.ref}>
            <PostPreview post={p} />
            <EngageBar post={p} actions={actions} />
            <p className="post-links">
              <Link to={`/p/${p.ref}`}>{t('posts.open')}</Link>
            </p>
          </li>
        ))}
      </ol>
      {q.hasNextPage ? (
        <button
          type="button"
          data-testid="person-posts-more"
          disabled={q.isFetchingNextPage}
          onClick={() => {
            void q.fetchNextPage();
          }}
        >
          {t('posts.more')}
        </button>
      ) : null}
    </section>
  );
}
