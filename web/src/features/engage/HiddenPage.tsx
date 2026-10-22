// /settings/hidden: what the viewer hid, with a way back. People they see
// less of come first (Show normally clears the count; their posts stay
// hidden), then the hidden posts they can still see, most recent first, each
// with Unhide. Trial feedback: hides and show less had no undo.
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { Loading } from '../../app/Loading';
import { errorText } from '../../api/errors';
import { PostPreview } from '../post/shared';
import { fetchHidden, fetchShowLess, hiddenKey, resetShowLess, unhidePost } from './api';
import './engage.css';

type State = 'pending' | 'done' | { err: unknown };

// useUndo runs one undo per ref and remembers how it went on this page.
function useUndo(call: (ref: string) => Promise<void>) {
  const qc = useQueryClient();
  const [states, setStates] = useState<Record<string, State>>({});
  const run = (ref: string) => {
    setStates((s) => ({ ...s, [ref]: 'pending' }));
    call(ref).then(
      () => {
        setStates((s) => ({ ...s, [ref]: 'done' }));
        void qc.invalidateQueries({ queryKey: hiddenKey, refetchType: 'none' });
      },
      (err: unknown) => {
        setStates((s) => ({ ...s, [ref]: { err } }));
      },
    );
  };
  return [states, run] as const;
}

function UndoError({ state }: { state: State | undefined }) {
  const { t } = useTranslation(['errors']);
  if (!state || typeof state === 'string') return null;
  return (
    <p className="engage-error" role="alert">
      {errorText(t, state.err)}
    </p>
  );
}

export default function HiddenPage() {
  const { t } = useTranslation(['engage', 'errors']);
  const less = useQuery({ queryKey: [...hiddenKey, 'less'], queryFn: fetchShowLess, retry: false });
  const posts = useInfiniteQuery({
    queryKey: [...hiddenKey, 'posts'],
    queryFn: ({ pageParam }) => fetchHidden(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const [lessStates, reset] = useUndo(resetShowLess);
  const [postStates, unhide] = useUndo(unhidePost);
  const items = posts.data?.pages.flatMap((p) => p.items) ?? [];
  const error = less.error ?? posts.error;
  return (
    <section className="page engage-page hidden-page">
      <h1>{t('hidden.title')}</h1>
      <p className="muted">{t('hidden.intro')}</p>
      {less.isPending || posts.isPending ? <Loading /> : null}
      {error ? (
        <p className="engage-error" role="alert">
          {errorText(t, error)}
        </p>
      ) : null}
      <h2>{t('hidden.less.title')}</h2>
      <p className="muted">{t('hidden.less.help')}</p>
      {less.isSuccess && less.data.length === 0 ? <p data-testid="hidden-less-empty">{t('hidden.less.empty')}</p> : null}
      <ul className="hidden-list" data-testid="hidden-less">
        {less.data?.map((p) => {
          const st = lessStates[p.ref];
          return (
            <li key={p.ref} className="hidden-item" data-testid="hidden-less-item" data-ref={p.ref}>
              <span>
                {p.displayName ? <span data-user-content="">{p.displayName} </span> : null}
                <span className="muted">@{p.handle}</span>{' '}
                <span className="muted">{t('hidden.less.count', { count: p.count })}</span>
              </span>
              {st === 'done' ? (
                <span role="status">{t('hidden.less.done')}</span>
              ) : (
                <button
                  type="button"
                  data-testid="hidden-less-reset"
                  disabled={st === 'pending'}
                  onClick={() => {
                    reset(p.ref);
                  }}
                >
                  {t('hidden.less.reset')}
                </button>
              )}
              <UndoError state={st} />
            </li>
          );
        })}
      </ul>
      <h2>{t('hidden.posts.title')}</h2>
      {posts.isSuccess && items.length === 0 ? <p data-testid="hidden-empty">{t('hidden.posts.empty')}</p> : null}
      <ol className="hidden-posts" data-testid="hidden-posts">
        {items.map((p) => {
          const st = postStates[p.ref];
          return (
            <li key={p.ref} className="hidden-post" data-testid="hidden-post" data-ref={p.ref}>
              <PostPreview post={p} />
              <p className="hidden-actions">
                {st === 'done' ? (
                  <span role="status">{t('hidden.posts.done')}</span>
                ) : (
                  <button
                    type="button"
                    data-testid="hidden-unhide"
                    disabled={st === 'pending'}
                    onClick={() => {
                      unhide(p.ref);
                    }}
                  >
                    {t('hidden.posts.unhide')}
                  </button>
                )}
                <Link to={`/p/${p.ref}`}>{t('page.open')}</Link>
              </p>
              <UndoError state={st} />
            </li>
          );
        })}
      </ol>
      {posts.hasNextPage ? (
        <button
          type="button"
          data-testid="hidden-more"
          disabled={posts.isFetchingNextPage}
          onClick={() => {
            void posts.fetchNextPage();
          }}
        >
          {t('hidden.posts.more')}
        </button>
      ) : null}
    </section>
  );
}
