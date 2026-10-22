// usePostActions is the PostActions every format receives (plan §6.3): like
// (optimistic, then the server's summary), save (optimistic, rolled back on
// failure; private, so nothing but saved_by_me changes), hide and show less (the post leaves
// the layouts at once through useHidden), seen (queued for the batched POST),
// navigation for reply and the family tree, and the report modal. Every cached
// view-model of the post is patched in place, whichever feature cached it.
// Owner: P0-W06 → P1-W08.
import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import { useMemo } from 'react';
import { useNavigate } from 'react-router';
import type { PostActions } from '../../formats/types';
import type { LikesVM, PostVM, Ref } from '../../vm/types';
import { clearPicks, composeURL, getPicks } from '../composer';
import { useOpenReport } from '../moderation/ReportModal';
import { savesKey } from '../saves/api';
import { queueSeen } from '../seen/tracker';
import { hidePost, likesKey, setLike, setSave, showLess } from './api';
import { setFailed, setHidden, type HiddenKind } from './store';
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
        if (before) applyLikes(qc, ref, optimistic(before, on));
        setLike(ref, on).then(
          (likes) => {
            applyLikes(qc, ref, likes);
          },
          (err: unknown) => {
            if (before) applyLikes(qc, ref, before);
            setFailed(ref, err);
          },
        );
      },
      save: (ref: Ref, on: boolean) => {
        setFailed(ref, undefined);
        applySaved(qc, ref, on);
        setSave(ref, on).then(
          // The open saves page keeps an unsaved post (to save it again); the
          // list is read afresh the next time it opens.
          () => qc.invalidateQueries({ queryKey: savesKey, refetchType: 'inactive' }),
          (err: unknown) => {
            applySaved(qc, ref, !on);
            setFailed(ref, err);
          },
        );
      },
      hide: hideAs('hide', hidePost),
      showLess: hideAs('show_less', showLess),
      markSeen: (ref: Ref) => {
        queueSeen(ref);
      },
      reply: (ref: Ref) => {
        // A reply takes the posts already picked for it along.
        const url = composeURL([...getPicks().map((p) => p.ref), ref]);
        clearPicks();
        void navigate(url);
      },
      openTree: (ref: Ref) => {
        void navigate(`/p/${ref}`);
      },
      report: openReport,
    };
  }, [qc, navigate, openReport]);
}
