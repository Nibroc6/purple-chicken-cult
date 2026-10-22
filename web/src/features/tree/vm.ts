// Tree view-models: the API's TreeView / TreeRow / TreeItem mapped once in the
// data layer (plan §6.2), and the pure state changes of an open tree (re-center,
// more, earlier and later rows). Posts go through the shared toPostVM (P0-W05)
// and are marked to fold at 10 lines in the tree (§10.1.5).
// Owner: P0-W08 → P1-W09.
import { ApiError, errorKey } from '../../api/errors';
import type { components } from '../../api/schema.gen';
import type { PostVM, Ref, TreeItemVM, TreeKinVM, TreeRowVM, TreeVM } from '../../vm/types';
import { asRef } from '../../vm/types';
import { codePoints, toHopVM, toPostVM } from '../post/vm';

type S = components['schemas'];

// treeKey prefixes every tree query (['tree', …]).
export const treeKey = ['tree'] as const;

// The tree folds a post past 10 lines (§10.1.5); a very long line counts as
// several (roughly 70 characters each), like the clamp the layout applies.
export const TREE_FOLD_LINES = 10;
const LINE_CHARS = 70;

export function treeFold(body: string): PostVM['fold'] {
  if (body === '') return 'none';
  const lines = body.split('\n').reduce((n, l) => n + Math.max(1, Math.ceil(codePoints(l) / LINE_CHARS)), 0);
  return lines > TREE_FOLD_LINES ? 'tree' : 'none';
}

export function toTreePostVM(p: S['PostView']): PostVM {
  return { ...toPostVM(p), fold: treeFold(p.body) };
}

export function toTreeItemVM(it: S['TreeItem']): TreeItemVM {
  return {
    post: toTreePostVM(it.post),
    tierDivider: it.tier_divider ?? false,
    shownAbove: it.shown_above ?? false,
    alsoRepliesTo: (it.also_replies_to ?? []).map(toHopVM),
    alsoParents: (it.also_parents ?? []).map(asRef),
  };
}

export function toTreeRowVM(r: S['TreeRow']): TreeRowVM {
  return {
    gen: r.gen,
    label: r.label,
    center: asRef(r.center),
    items: r.items.map(toTreeItemVM),
    more: r.more,
    ...(r.cursor ? { cursor: r.cursor } : {}),
    ...(r.beyond ? { beyond: true } : {}),
  };
}

export function toTreeKinVM(k: S['TreeKin']): TreeKinVM {
  return {
    post: toTreePostVM(k.post),
    parent: asRef(k.parent),
    gen: k.gen,
    degree: k.degree,
    alsoParents: (k.also_parents ?? []).map(asRef),
  };
}

export function toTreeVM(v: S['TreeView']): TreeVM {
  return {
    snapshot: v.snapshot,
    focal: asRef(v.focal),
    rows: v.rows.map(toTreeRowVM),
    kin: (v.kin ?? []).map(toTreeKinVM),
    hasEarlier: v.has_earlier,
    hasLater: v.has_later,
  };
}

// ---- state of an open tree ----

const byGen = (rows: TreeRowVM[]) => [...rows].sort((a, b) => a.gen - b.gen);

export function rowAt(tree: TreeVM, gen: number): TreeRowVM | undefined {
  return tree.rows.find((r) => r.gen === gen);
}

export function topRow(tree: TreeVM): TreeRowVM | undefined {
  return byGen(tree.rows)[0];
}

export function bottomRow(tree: TreeVM): TreeRowVM | undefined {
  return byGen(tree.rows).at(-1);
}

// laterFrom: a row's centered post has visible replies (reply counts are built
// from visible links only).
export function laterFrom(row: TreeRowVM | undefined, center: Ref | undefined): boolean {
  const it = row?.items.find((i) => i.post.ref === center) ?? row?.items[0];
  return (it?.post.replyCount ?? 0) > 0;
}

// putRow sets row.gen and, when below the focal row, drops the rows below it
// (they belonged to the old center). An empty row is not kept.
export function putRow(tree: TreeVM, row: TreeRowVM): TreeVM {
  const rows = tree.rows.filter((r) => r.gen !== row.gen && !(row.gen >= 1 && r.gen > row.gen));
  if (row.items.length > 0) rows.push(row);
  return { ...tree, rows: byGen(rows) };
}

// appendMore adds the next page of a row (the cursor and count move on).
export function appendMore(tree: TreeVM, page: TreeRowVM): TreeVM {
  return {
    ...tree,
    rows: tree.rows.map((r) => {
      if (r.gen !== page.gen || r.center !== page.center) return r;
      const out: TreeRowVM = { gen: r.gen, label: r.label, center: r.center, items: [...r.items, ...page.items], more: page.more };
      if (page.cursor) out.cursor = page.cursor;
      return out;
    }),
  };
}

// addEarlier puts a row above the top row. Each post appears once: a post of
// the new row already shown in a row below becomes "↑ shown above" there.
// Earlier rows remain when the server says the new row's first post has
// parents of its own (beyond).
export function addEarlier(tree: TreeVM, row: TreeRowVM): TreeVM {
  const next = putRow(tree, row);
  const added = new Set(row.items.map((i) => i.post.ref));
  const rows = next.rows.map((r) =>
    r.gen > row.gen && r.items.some((i) => added.has(i.post.ref))
      ? { ...r, items: r.items.map((i) => (added.has(i.post.ref) ? { ...i, shownAbove: true } : i)) }
      : r,
  );
  return { ...next, rows, hasEarlier: row.items.length > 0 && row.beyond === true };
}

// isExpired: the server no longer keeps the snapshot (the page reopens the tree).
export function isExpired(err: unknown): boolean {
  return err instanceof ApiError && err.code === 'tree_snapshot_expired';
}

// treeErrorKey localizes a tree API error.
// i18n-dynamic: tree:errors.
export function treeErrorKey(err: unknown): string {
  if (err instanceof ApiError && err.isNotFound) return 'tree:errors.not_found';
  if (isExpired(err)) return 'tree:errors.tree_snapshot_expired';
  return errorKey(err);
}
