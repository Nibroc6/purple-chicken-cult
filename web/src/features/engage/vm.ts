// Like-summary mapping and the structural cache patcher shared by the engage
// actions. Owner: P0-W06 → P1-W08.
import type { components } from '../../api/schema.gen';
import type { LikesVM, PostVM } from '../../vm/types';
import { toHopVM } from '../post/vm';

type S = components['schemas'];

export function toLikesVM(l: S['LikeSummary']): LikesVM {
  return { inNetwork: l.in_network, names: (l.names ?? []).map(toHopVM), others: l.others, byMe: l.by_me };
}

// optimistic is the summary right after the viewer toggles their own like,
// before the server answers: the viewer counts in their own network.
export function optimistic(l: LikesVM, on: boolean): LikesVM {
  if (l.byMe === on) return l;
  return { ...l, byMe: on, inNetwork: Math.max(0, l.inNetwork + (on ? 1 : -1)) };
}

function isPostVM(x: object): x is PostVM {
  return 'ref' in x && 'likes' in x && 'author' in x && typeof (x as { ref: unknown }).ref === 'string';
}

// patchLikes returns data with the likes of every PostVM whose ref is ref
// replaced (structurally shared: untouched branches keep their identity, and
// data itself is returned when nothing matched). It walks plain objects and
// arrays only, so it works for any feature's cached view-models (feed pages,
// tree rows, people's posts, a single post).
export function patchLikes<T>(data: T, ref: string, likes: LikesVM): T {
  const walk = (x: unknown, depth: number): unknown => {
    if (depth > 8 || x === null || typeof x !== 'object') return x;
    if (Array.isArray(x)) {
      const out = x.map((v) => walk(v, depth + 1));
      return out.some((v, i) => v !== x[i]) ? out : x;
    }
    if (Object.getPrototypeOf(x) !== Object.prototype) return x; // Dates, Maps, class instances
    if (isPostVM(x) && x.ref === ref) return { ...x, likes };
    let changed = false;
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(x)) {
      const n = walk(v, depth + 1);
      if (n !== v) changed = true;
      out[k] = n;
    }
    return changed ? out : x;
  };
  return walk(data, 0) as T;
}
