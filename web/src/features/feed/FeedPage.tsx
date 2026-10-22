// The feed (/ when signed in; ARCHITECTURE §4, §10.1.7). One session per visit:
// the first page starts it, "load more" follows its cursor, so pages never
// reshuffle. Every 60 s while the page is visible it asks how many posts are
// new in the tiers already reached and offers to start over ("N new posts
// above"). Latest / Balanced / Top restarts the feed in that order and saves it. A
// session the server no longer keeps (409 feed_session_expired) restarts.
// The layout comes from the reader's format (Cards or Compact); headers are the
// API's tier markers only. Owner: P0-W07 → P1-W08.
import { useInfiniteQuery, useQuery, useQueryClient, type InfiniteData } from '@tanstack/react-query';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { onFeedSessionExpired } from '../../api/client';
import { errorText } from '../../api/errors';
import { Loading } from '../../app/Loading';
import { sessionKey, useMe } from '../../app/session';
import { formats } from '../../formats/registry';
import type { FormatId, FormatModule } from '../../formats/types';
import { WhyLoaderContext, type WhyLoader } from '../../integrity/types';
import type { FeedItemVM, FeedPageVM, Preset } from '../../vm/types';
import { usePostActions } from '../engage';
import { patchSettings } from '../settings/api';
import { accountMeKey, useAccountMe } from '../settings/shared';
import { toUIPrefs } from '../settings/vm';
import { expandGroup, fetchFeed, fetchNewAbove, fetchWhy } from './api';
import { countPosts, expandInPages, feedKey, ownPostsKey, presets, savedPreset, withOwnPosts } from './vm';
import './feed.css';
import { InfoTip } from '../../lib/InfoTip';

export const NEW_ABOVE_MS = 60_000;

type Pages = InfiniteData<FeedPageVM, string | undefined>;

// useFormat loads the reader's layout format chunk.
function useFormat(id: FormatId, enabled: boolean): FormatModule | undefined {
  const q = useQuery({
    queryKey: ['format', id],
    enabled,
    queryFn: async () => (await formats[id]()).default,
    staleTime: Infinity,
    structuralSharing: false,
  });
  return q.data;
}

// i18n-dynamic: feed:preset.
function PresetSwitch({ value, onChange }: { value: Preset; onChange: (p: Preset) => void }) {
  const { t } = useTranslation('feed');
  return (
    <div className="feed-presets" role="group" aria-label={t('preset.label')}>
      {presets.map((p) => (
        <button
          key={p}
          type="button"
          data-testid={`feed-preset-${p}`}
          aria-pressed={value === p}
          onClick={() => {
            if (p !== value) onChange(p);
          }}
        >
          {t(`preset.${p}` as 'preset.latest')}
        </button>
      ))}
      <InfoTip label={t('preset.hintLabel')} testId="feed-preset-hint">
        {t('preset.hint', { preset: value })}
      </InfoTip>
    </div>
  );
}

export default function FeedPage() {
  const { t } = useTranslation(['feed', 'errors']);
  const me = useMe();
  const account = useAccountMe(); // every setting (the session view carries a summary)
  const qc = useQueryClient();
  const actions = usePostActions();
  const settings = account.data?.settings ?? me?.settings;
  const saved = savedPreset(settings?.rank_preset);
  const [chosen, setChosen] = useState<Preset | undefined>(undefined);
  const preset = chosen ?? saved;
  const [gen, setGen] = useState(0);
  const [expandError, setExpandError] = useState<unknown>(undefined);
  const key = useMemo(() => [...feedKey, 'pages', gen] as const, [gen]);
  // The layout waits for the reader's format (Cards when it cannot be read).
  const format = useFormat(toUIPrefs(account.data?.settings.ui_prefs).format, !account.isPending);

  const q = useInfiniteQuery({
    queryKey: key,
    queryFn: ({ pageParam }) => fetchFeed(pageParam ? undefined : chosen, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next,
    retry: false,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  const pages = q.data?.pages;
  const session = pages?.[0]?.session;
  // D16: what the reader published this visit, shown above the feed.
  const own = useQuery<FeedItemVM[]>({
    queryKey: ownPostsKey,
    queryFn: () => qc.getQueryData<FeedItemVM[]>(ownPostsKey) ?? [],
    initialData: [],
    staleTime: Infinity,
  }).data;
  const shown = useMemo(() => (pages ? withOwnPosts(pages, own) : undefined), [pages, own]);

  const restart = useCallback(() => {
    setExpandError(undefined);
    qc.removeQueries({ queryKey: [...feedKey, 'pages'] });
    setGen((g) => g + 1);
    window.scrollTo(0, 0);
  }, [qc]);

  useEffect(() => onFeedSessionExpired(restart), [restart]);

  const fresh = useQuery({
    queryKey: [...feedKey, 'new', gen, session],
    queryFn: () => fetchNewAbove(session ?? ''),
    enabled: session !== undefined,
    refetchInterval: NEW_ABOVE_MS,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: true,
    staleTime: NEW_ABOVE_MS,
    initialData: { count: 0, tier: 0 },
    initialDataUpdatedAt: () => Date.now(),
    retry: false,
  });

  const changePreset = (p: Preset) => {
    setChosen(p);
    restart();
    patchSettings({ rank_preset: p }).then(
      () => Promise.all([qc.invalidateQueries({ queryKey: sessionKey }), qc.invalidateQueries({ queryKey: accountMeKey })]),
      () => undefined, // the feed already uses it; the saved default stays as it was
    );
  };

  const onExpand = (token: string) => {
    setExpandError(undefined);
    expandGroup(token).then(
      (items) => {
        qc.setQueryData<Pages>(key, (old) => (old ? { ...old, pages: expandInPages(old.pages, token, items) } : old));
      },
      (err: unknown) => {
        setExpandError(err);
      },
    );
  };

  const why = useCallback<WhyLoader>(
    (ref) => qc.query({ queryKey: [...feedKey, 'why', ref], queryFn: () => fetchWhy(ref), staleTime: NEW_ABOVE_MS }),
    [qc],
  );

  const caughtUpEmpty = q.isSuccess && shown !== undefined && countPosts(shown) === 0 && !q.hasNextPage;
  return (
    <section className="page feed-page" data-testid="feed-page" data-session-gen={gen}>
      <h1 className="visually-hidden">{t('page.title')}</h1>
      <PresetSwitch value={preset} onChange={changePreset} />
      {fresh.data.count > 0 ? (
        <button type="button" className="feed-new-above" data-testid="feed-new-above" data-count={fresh.data.count} onClick={restart}>
          {t('newAbove.banner', { count: fresh.data.count })}
        </button>
      ) : null}
      {q.isPending || !format ? <Loading /> : null}
      {q.error ? (
        <div className="feed-error" role="alert" data-testid="feed-error">
          <p>{errorText(t, q.error)}</p>
          <button type="button" onClick={restart}>
            {t('page.retry')}
          </button>
        </div>
      ) : null}
      {caughtUpEmpty ? (
        <p className="muted" data-testid="feed-empty">
          {t('page.empty')}
          <InfoTip>{t('page.emptyTip')}</InfoTip>
        </p>
      ) : null}
      {expandError ? (
        <p className="feed-error" role="alert">
          {t('page.expandError')}
        </p>
      ) : null}
      {shown && format ? (
        <WhyLoaderContext.Provider value={why}>
          <format.FeedLayout
            page={shown}
            actions={actions}
            onLoadMore={() => {
              if (q.hasNextPage && !q.isFetchingNextPage) void q.fetchNextPage();
            }}
            onExpand={onExpand}
          />
        </WhyLoaderContext.Provider>
      ) : null}
      {q.isFetchingNextPage ? <Loading /> : null}
    </section>
  );
}
