// Friends list (§4). Owner: P0-W03 → P1-W03.
import { useInfiniteQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Loading } from '../../app/Loading';
import { fetchFriends } from './api';
import { ErrorText, PeopleList, PeopleNav, peopleKey } from './shared';

export default function FriendsPage() {
  const { t } = useTranslation('people');
  const q = useInfiniteQuery({
    queryKey: [...peopleKey, 'friends'],
    queryFn: ({ pageParam }) => fetchFriends(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next,
  });
  const people = q.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <section className="page people-page">
      <h1>{t('friends.title')}</h1>
      <PeopleNav />
      {q.isPending ? <Loading /> : q.error ? <ErrorText err={q.error} /> : <PeopleList people={people} empty={t('friends.empty')} emptyTip={t('friends.emptyTip')} />}
      {q.hasNextPage ? (
        <button
          type="button"
          disabled={q.isFetchingNextPage}
          onClick={() => {
            void q.fetchNextPage();
          }}
        >
          {t('friends.more')}
        </button>
      ) : null}
    </section>
  );
}
