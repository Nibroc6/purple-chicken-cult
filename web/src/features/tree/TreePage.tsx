// The reply tree (/p/:ref; ARCHITECTURE §10.1.4): the post as the focal post,
// two generations of parents above and two of replies below, each ranked once
// per snapshot like a feed tier, all on one connected canvas (the format's
// TreeLayout). Choosing another post opens the tree around it, keeping the
// current tree on screen until the new one arrives so the canvas can move
// over to it; "+N more" pages through a generation; earlier and later
// generations load at the edges. A snapshot the server no longer keeps (409
// tree_snapshot_expired) reopens the tree. Every other failure is the uniform
// "not available". Leaving the page drops the tree, so returning opens a new
// snapshot. Owner: P0-W08 → P1-W09.
import { errorText } from '../../api/errors';
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams } from 'react-router';
import { Loading } from '../../app/Loading';
import type { TreeActions } from '../../formats/types';
import { useFormat } from '../../formats/useFormat';
import { WhyLoaderContext, type WhyLoader } from '../../integrity/types';
import type { Ref, TreeRowVM, TreeVM } from '../../vm/types';
import { usePostActions } from '../engage';
import { fetchWhy } from '../feed/api';
import { useAccountMe } from '../settings/shared';
import { toUIPrefs } from '../settings/vm';
import { fetchMore, fetchRow, openTree } from './api';
import { addEarlier, appendMore, bottomRow, isExpired, laterFrom, putRow, topRow, treeErrorKey, treeKey } from './vm';
import './tree.css';

export default function TreePage() {
  const { t } = useTranslation(['tree', 'errors']);
  const { ref = '' } = useParams();
  const qc = useQueryClient();
  const post = usePostActions();
  const account = useAccountMe();
  const format = useFormat(toUIPrefs(account.data?.settings.ui_prefs).format, !account.isPending);
  const [opened, setOpened] = useState(0);
  const key = useMemo(() => [...treeKey, 'open', ref, opened] as const, [ref, opened]);
  // An action's failure belongs to the tree it happened in.
  const [failed, setFailed] = useState<{ key: typeof key; err: unknown } | undefined>(undefined);
  const actionError = failed?.key === key ? failed.err : undefined;

  const q = useQuery({
    queryKey: key,
    queryFn: () => openTree(ref),
    retry: false,
    // A snapshot is frozen while the page is open, but coming back to the post
    // (after replying, or later from the feed) opens a fresh tree: a cached one
    // would never show the replies posted since.
    staleTime: Infinity,
    gcTime: 0,
    // Another post of the tree: the current one stays until the new one is in.
    placeholderData: keepPreviousData,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });

  const reopen = useCallback(() => {
    qc.removeQueries({ queryKey: [...treeKey, 'open', ref] });
    setOpened((n) => n + 1);
  }, [qc, ref]);

  const actions = useMemo<TreeActions>(() => {
    const current = () => qc.getQueryData<TreeVM>(key);
    const update = (fn: (t: TreeVM) => TreeVM) => {
      qc.setQueryData<TreeVM>(key, (old) => (old ? fn(old) : old));
    };
    const fail = (err: unknown) => {
      if (isExpired(err)) reopen();
      else setFailed({ key, err });
    };
    // A generation hangs from its first post (the server's best-ranked one).
    const centerOf = (row: TreeRowVM): Ref | undefined => row.items.find((i) => !i.shownAbove)?.post.ref ?? row.items[0]?.post.ref;
    // loadBelow loads the replies of center as row gen+1.
    const loadBelow = async (snapshot: string, gen: number, center: Ref) => {
      const row = await fetchRow(snapshot, gen + 1, center);
      update((tr) => ({ ...putRow(tr, row), hasLater: laterFrom(row, centerOf(row)) }));
    };
    return {
      ...post,
      more: (gen: number, center: Ref, cursor: string) => {
        const tree = current();
        if (!tree) return;
        fetchMore(tree.snapshot, gen, center, cursor).then((page) => {
          update((tr) => appendMore(tr, page));
        }, fail);
      },
      loadEarlier: () => {
        const tree = current();
        const top = tree ? topRow(tree) : undefined;
        const c = top ? centerOf(top) : undefined;
        if (!tree || !top || !c) return;
        fetchRow(tree.snapshot, top.gen - 1, c).then((row) => {
          update((tr) => addEarlier(tr, row));
        }, fail);
      },
      loadLater: () => {
        const tree = current();
        const bottom = tree ? bottomRow(tree) : undefined;
        const c = bottom ? centerOf(bottom) : undefined;
        if (!tree || !bottom || !c) return;
        loadBelow(tree.snapshot, bottom.gen, c).catch(fail);
      },
    };
  }, [post, qc, key, reopen]);

  const why = useCallback<WhyLoader>(
    (r) => qc.query({ queryKey: [...treeKey, 'why', r], queryFn: () => fetchWhy(r), staleTime: 60_000 }),
    [qc],
  );

  return (
    <section className="page tree-page" data-testid="tree-page" data-opened={opened} aria-busy={q.isPlaceholderData}>
      <h1 className="visually-hidden">{t('page.title')}</h1>
      {q.isPending || (q.data && !format) ? <Loading /> : null}
      {q.error ? (
        <div className="tree-error" role="alert" data-testid="tree-error">
          <p>{errorText(t, q.error, treeErrorKey)}</p>
          {isExpired(q.error) ? null : (
            <button type="button" onClick={reopen}>
              {t('page.retry')}
            </button>
          )}
        </div>
      ) : null}
      {actionError ? (
        <p className="tree-error" role="alert" data-testid="tree-action-error">
          {errorText(t, actionError, treeErrorKey)}
        </p>
      ) : null}
      {q.data && format ? (
        <WhyLoaderContext.Provider value={why}>
          <format.TreeLayout tree={q.data} actions={actions} />
        </WhyLoaderContext.Provider>
      ) : null}
    </section>
  );
}
