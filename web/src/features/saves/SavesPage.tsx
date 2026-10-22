// /saves: the posts the viewer saved and can still see, most recently saved
// first. Saves are private: nobody else sees them or how many a post has.
// An unsaved post stays in the list until the page reloads, so it can be
// saved again. Owner: P1-W08.
import { useInfiniteQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Loading } from '../../app/Loading';
import { errorText } from '../../api/errors';
import { usePostActions } from '../engage/usePostActions';
import { PostCard } from '../post/PostCard';
import { fetchSaves, savesKey } from './api';
import { PostListsNav } from '../../app/PostListsNav';
import './saves.css';

export default function SavesPage() {
  const { t } = useTranslation(['saves', 'errors']);
  const actions = usePostActions();
  const q = useInfiniteQuery({
    queryKey: savesKey,
    queryFn: ({ pageParam }) => fetchSaves(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const posts = q.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <section className="page saves-page">
      <h1>{t('page.title')}</h1>
      <PostListsNav />
      <p className="muted">{t('page.intro')}</p>
      {q.isPending ? <Loading /> : null}
      {q.error ? (
        <p className="saves-error" role="alert">
          {errorText(t, q.error)}
        </p>
      ) : null}
      {q.isSuccess && posts.length === 0 ? <p data-testid="saves-empty">{t('page.empty')}</p> : null}
      <ol className="saves-list" data-testid="saves-list">
        {posts.map((p) => (
          <li key={p.ref} className="saves-item" data-testid="saves-item" data-ref={p.ref}>
            <PostCard post={p} actions={actions} />
          </li>
        ))}
      </ol>
      {q.hasNextPage ? (
        <button
          type="button"
          data-testid="saves-more"
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
