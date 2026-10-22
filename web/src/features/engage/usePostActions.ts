// usePostActions is the PostActions every format receives (plan §6.3): like
// (optimistic, then the server's summary), save (optimistic, rolled back on
// failure; private, so nothing but saved_by_me changes), both coalesced per
// post (toggles.ts: quick on/off clicks send at most what changed), hide and show less (the post leaves
// the layouts at once through useHidden), seen (queued for the batched POST),
// navigation for reply and the family tree, and the report modal. Every cached
// view-model of the post is patched in place, whichever feature cached it.
// Owner: P0-W06 → P1-W08.
import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import { useMemo } from 'react';
import { useNavigate } from 'react-router';
import type { PostActions } from '../../formats/types';
import type { LikesVM, PostVM, Ref } from '../../vm/types';
import { useOpenReport } from '../moderation/ReportModal';
import { savesKey } from '../saves/api';
import { queueSeen } from '../seen/tracker';
import { hidePost, likesKey, setLike, setSave, showLess } from './api';
import { setFailed, setHidden, type HiddenKind } from './store';
import { makeToggler, type Toggler } from './toggles';
import { optimistic, patchLikes, patchSaved } from './vm';

// currentLikes finds the post's like summary in any cached query.
function currentLikes(qc: QueryClient, ref: string): LikesVM | undefined {
  const own = qc.getQueryData<LikesVM>([...likesKey, ref]);
  if (own) return own;
  let found: LikesVM | undefined;
  const look = (x: unknown, depth: number): void => {
    if (found || depth > 8 || x === null || typeof x !== 'object') return;
    if (Array.isArray(x)) {
      x.forEach((v) => {
        look(v, depth + 1);
      });
      return;
    }
    if (Object.getPrototypeOf(x) !== Object.prototype) return;
    const p = x as Partial<PostVM>;
    if (p.ref === ref && p.likes && p.author) {
      found = p.likes;
      return;
    }
    Object.values(x).forEach((v) => {
      look(v, depth + 1);
    });
  };
  qc.getQueryCache()
    .getAll()
    .forEach((q) => {
      look(q.state.data, 0);
    });
  return found;
}

// applyLikes writes a summary into every cached view-model of the post.
export function applyLikes(qc: QueryClient, ref: string, likes: LikesVM): void {
  qc.setQueryData([...likesKey, ref], likes);
  qc.getQueryCache()
    .getAll()
    .forEach((q) => {
      if (q.queryKey[0] === likesKey[0]) return;
      const next = patchLikes(q.state.data, ref, likes);
      if (next !== q.state.data) qc.setQueryData(q.queryKey, next);
    });
}

// applySaved writes saved_by_me into every cached view-model of the post.
export function applySaved(qc: QueryClient, ref: string, saved: boolean): void {
  qc.getQueryCache()
    .getAll()
    .forEach((q) => {
      const next = patchSaved(q.state.data, ref, saved);
      if (next !== q.state.data) qc.setQueryData(q.queryKey, next);
    });
}

// togglers holds the like and save togglers of each query client, so every
// control of one post shares its pending state.
// A failed like goes back to the summary from before the first pending click.
interface Togglers {
  like: Toggler;
  save: Toggler;
  likeBase: Map<string, LikesVM>;
}

const togglers = new WeakMap<QueryClient, Togglers>();

function togglersFor(qc: QueryClient): Togglers {
  let t = togglers.get(qc);
  if (!t) {
    const likeBase = new Map<string, LikesVM>();
    t = {
      likeBase,
      like: makeToggler({
        // Each answer is the new base a later failure goes back to.
        send: (ref, on) =>
          setLike(ref, on).then((likes) => {
            likeBase.set(ref, likes);
            return likes;
          }),
        landed: (ref, _on, likes) => {
          applyLikes(qc, ref, likes);
        },
        failed: (ref, _confirmed, err) => {
          const base = likeBase.get(ref);
          if (base) applyLikes(qc, ref, base);
          setFailed(ref, err);
        },
        settled: (ref) => {
          likeBase.delete(ref);
        },
      }),
      save: makeToggler({
        send: setSave,
        // The open saves page keeps an unsaved post (to save it again); the
        // list is read afresh the next time it opens.
        landed: () => void qc.invalidateQueries({ queryKey: savesKey, refetchType: 'inactive' }),
        failed: (ref, confirmed, err) => {
          applySaved(qc, ref, confirmed);
          setFailed(ref, err);
        },
      }),
    };
    togglers.set(qc, t);
  }
  return t;
}

export function usePostActions(): PostActions {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const openReport = useOpenReport();
  return useMemo(() => {
    const hideAs = (kind: HiddenKind, call: (ref: string) => Promise<void>) => (ref: Ref) => {
      setFailed(ref, undefined);
      setHidden(ref, kind);
      call(ref).catch((err: unknown) => {
        setHidden(ref, undefined);
        setFailed(ref, err);
      });
    };
    return {
      like: (ref: Ref, on: boolean) => {
        setFailed(ref, undefined);
        const before = currentLikes(qc, ref);
        const tg = togglersFor(qc);
        if (before) {
          if (!tg.likeBase.has(ref)) tg.likeBase.set(ref, before);
          applyLikes(qc, ref, optimistic(before, on));
        }
        tg.like.set(ref, on, tg.likeBase.get(ref)?.byMe ?? !on);
      },
      save: (ref: Ref, on: boolean) => {
        setFailed(ref, undefined);
        applySaved(qc, ref, on);
        togglersFor(qc).save.set(ref, on, !on);
      },
      hide: hideAs('hide', hidePost),
      showLess: hideAs('show_less', showLess),
      markSeen: (ref: Ref) => {
        queueSeen(ref);
      },
      reply: (ref: Ref) => {
        void navigate(`/compose?${new URLSearchParams({ parent: ref, kind: 'ref' }).toString()}`);
      },
      openTree: (ref: Ref) => {
        void navigate(`/p/${ref}`);
      },
      report: openReport,
    };
  }, [qc, navigate, openReport]);
}
