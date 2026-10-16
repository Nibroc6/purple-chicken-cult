// The family tree (/p/:ref; ARCHITECTURE §10.1.4): the post as the focal post,
// two rows of parents above and two rows of replies below, each ranked once
// per snapshot like a feed tier. Swiping a row re-centers it and the rows below
// reload as the new center's replies (re-centering "Replying to" reloads the
// focal row's siblings under that parent); "+N more" pages through a row;
// earlier and later rows load at the edges. A snapshot the server no longer
// keeps (409 tree_snapshot_expired) reopens the tree. Every other failure is
// the uniform "not available". The layout comes from the reader's format.
// Owner: P0-W08 → P1-W09.
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams } from 'react-router';
import { Loading } from '../../app/Loading';
import { formats } from '../../formats/registry';
import type { FormatId, FormatModule, TreeActions } from '../../formats/types';
import { WhyLoaderContext, type WhyLoader } from '../../integrity/types';
import type { Ref, TreeVM } from '../../vm/types';
import { usePostActions } from '../engage';
import { fetchWhy } from '../feed/api';
import { useAccountMe } from '../settings/shared';
import { toUIPrefs } from '../settings/vm';
import { fetchMore, fetchRow, openTree } from './api';
import { addEarlier, appendMore, bottomRow, isExpired, laterFrom, putRow, topRow, treeErrorKey, treeKey } from './vm';
import './tree.css';

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

// DEPTH is how far below the focal row a re-centered row reloads at least.
const DEPTH = 2;

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
  // The centered post of each row (the first one until the reader picks another).
  const centers = useMemo(() => ({ key, current: new Map<number, Ref>() }), [key]);

  const q = useQuery({
    queryKey: key,
    queryFn: () => openTree(ref),
    retry: false,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });

  // Bring the focal row into view once the tree is there.
  const ready = q.data !== undefined && format !== undefined;
  useEffect(() => {
    if (!ready) return;
    const row: Partial<Element> | null = document.querySelector('[data-testid="tree-row"][data-gen="0"]');
    row?.scrollIntoView?.({ block: 'center' });
  }, [ready, key]);

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
    const centerOf = (gen: number): Ref | undefined => {
      const tree = current();
      const row = tree?.rows.find((r) => r.gen === gen);
      return centers.current.get(gen) ?? row?.items[0]?.post.ref;
    };
    // reloadBelow loads the replies of center as row gen+1 and follows the
    // first post of each new row down to the deepest row shown before.
    const reloadBelow = async (snapshot: string, gen: number, center: Ref, until: number) => {
      let c: Ref | undefined = center;
      for (let k = gen + 1; k <= until && c; k++) {
        const row = await fetchRow(snapshot, k, c);
        centers.current.delete(k);
        update((tr) => ({ ...putRow(tr, row), hasLater: laterFrom(row, row.items[0]?.post.ref) }));
        c = row.items[0]?.post.ref;
      }
    };
    return {
      ...post,
      recenter: (gen: number, center: Ref) => {
        const tree = current();
        if (!tree) return;
        setFailed(undefined);
        centers.current.set(gen, center);
        if (gen <= -2) return; // rows above stay put
        if (gen === -1) {
          // Another parent: the focal row lists the replies under it.
          fetchRow(tree.snapshot, 0, center).then((row) => {
            update((tr) => putRow(tr, row));
          }, fail);
          return;
        }
        const until = Math.max(DEPTH, bottomRow(tree)?.gen ?? 0);
        reloadBelow(tree.snapshot, gen, center, until).catch(fail);
      },
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
        const c = top ? centerOf(top.gen) : undefined;
        if (!tree || !top || !c) return;
        fetchRow(tree.snapshot, top.gen - 1, c).then((row) => {
          update((tr) => addEarlier(tr, row));
        }, fail);
      },
      loadLater: () => {
        const tree = current();
        const bottom = tree ? bottomRow(tree) : undefined;
        const c = bottom ? centerOf(bottom.gen) : undefined;
        if (!tree || !bottom || !c) return;
        reloadBelow(tree.snapshot, bottom.gen, c, bottom.gen + 1).catch(fail);
      },
    };
  }, [post, qc, key, reopen, centers]);

  const why = useCallback<WhyLoader>(
    (r) => qc.query({ queryKey: [...treeKey, 'why', r], queryFn: () => fetchWhy(r), staleTime: 60_000 }),
    [qc],
  );

  return (
    <section className="page tree-page" data-testid="tree-page" data-opened={opened}>
      <h1 className="visually-hidden">{t('page.title')}</h1>
      {q.isPending || (q.data && !format) ? <Loading /> : null}
      {q.error ? (
        <div className="tree-error" role="alert" data-testid="tree-error">
          <p>{t(treeErrorKey(q.error) as 'errors:unknown')}</p>
          {isExpired(q.error) ? null : (
            <button type="button" onClick={reopen}>
              {t('page.retry')}
            </button>
          )}
        </div>
      ) : null}
      {actionError ? (
        <p className="tree-error" role="alert" data-testid="tree-action-error">
          {t(treeErrorKey(actionError) as 'errors:unknown')}
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
