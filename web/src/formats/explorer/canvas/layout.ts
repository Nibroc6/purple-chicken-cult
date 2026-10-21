// Node layout of the Explorer canvas (pure; §15.1 Explorer, §10.1.4). One band
// per generation, top to bottom as the tree's rows come; in each band the
// row's centered post sits on the vertical axis and the others alternate right
// and left of it in ranked order, so the axis runs through the centered post of
// every generation like the Cards rows. Edges follow the rows' meaning: a row
// at or below the focal row holds replies to the centered post of the row
// above; a row above holds parents of the post below it (the focal post for
// "replying to", the centered parent further up). A post already shown higher
// up ("shown above", a diamond) gets no second node: its edge goes, dashed, to
// the node it already has. Owner: P1-W09.
import type { Ref, TreeRowVM, TreeVM } from '../../../vm/types';

export const NODE_W = 168;
export const NODE_H = 56;
export const GAP_X = 24;
export const ROW_H = 112;
export const PAD = 48;

export interface CanvasNode {
  ref: Ref;
  gen: number;
  x: number; // center
  y: number; // center
  centered: boolean;
  focal: boolean;
  tier: number;
  handle: string;
  excerpt: string;
}

export interface CanvasEdge {
  from: Ref; // upper end
  to: Ref; // lower end
  dashed: boolean;
}

export interface CanvasMore {
  gen: number;
  x: number;
  y: number;
  count: number;
}

export interface Bounds {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface CanvasLayout {
  nodes: CanvasNode[];
  edges: CanvasEdge[];
  more: CanvasMore[];
  bounds: Bounds;
  byRef: Map<Ref, CanvasNode>;
}

// slotOffset is a slot's place on the axis: 0, then +1, -1, +2, -2, …
export function slotOffset(rank: number): number {
  if (rank === 0) return 0;
  const step = Math.ceil(rank / 2);
  return rank % 2 === 1 ? step : -step;
}

const EXCERPT = 48;

// excerpt is the first words of a post body's text (the parsed Markdown tree;
// anything without text yields nothing).
export function excerpt(nodes: readonly unknown[] | undefined): string {
  let out = '';
  const walk = (n: unknown): void => {
    if (out.length > EXCERPT) return;
    if (typeof n === 'string') {
      out += `${n} `;
      return;
    }
    if (n === null || typeof n !== 'object') return;
    const o = n as { v?: unknown; text?: unknown; c?: unknown; lines?: unknown; items?: unknown };
    if (typeof o.v === 'string') out += o.v;
    else if (typeof o.text === 'string') out += o.text;
    for (const k of [o.c, o.lines, o.items]) if (Array.isArray(k)) k.forEach(walk);
    out += ' ';
  };
  (nodes ?? []).forEach(walk);
  const s = out.replace(/\s+/g, ' ').trim();
  return s.length > EXCERPT ? `${s.slice(0, EXCERPT - 1).trimEnd()}…` : s;
}

// centerOf is the centered post of a row: the chosen one, else its first post.
export type Centers = (row: TreeRowVM) => Ref | undefined;

export function layoutTree(tree: TreeVM, centered: Centers): CanvasLayout {
  const nodes: CanvasNode[] = [];
  const edges: CanvasEdge[] = [];
  const more: CanvasMore[] = [];
  const byRef = new Map<Ref, CanvasNode>();
  const centers = new Map<number, Ref>();
  for (const row of tree.rows) {
    const c = centered(row) ?? row.items.find((i) => !i.shownAbove)?.post.ref;
    if (c) centers.set(row.gen, c);
  }

  tree.rows.forEach((row, ri) => {
    const y = ri * ROW_H;
    const center = centers.get(row.gen);
    // The centered post first, then the rest in ranked order.
    const own = row.items.filter((i) => !i.shownAbove);
    const ordered = [...own.filter((i) => i.post.ref === center), ...own.filter((i) => i.post.ref !== center)];
    let maxOffset = 0;
    ordered.forEach((it, rank) => {
      if (byRef.has(it.post.ref)) return;
      const off = slotOffset(rank);
      maxOffset = Math.max(maxOffset, off);
      const n: CanvasNode = {
        ref: it.post.ref,
        gen: row.gen,
        x: off * (NODE_W + GAP_X),
        y,
        centered: it.post.ref === center,
        focal: it.post.ref === tree.focal,
        tier: it.post.tier,
        handle: it.post.author.handle,
        excerpt: excerpt(it.post.body?.nodes),
      };
      nodes.push(n);
      byRef.set(n.ref, n);
    });
    if (row.more > 0) more.push({ gen: row.gen, x: (maxOffset + 1) * (NODE_W + GAP_X), y, count: row.more });
  });

  const seen = new Set<string>();
  const add = (from: Ref | undefined, to: Ref | undefined, dashed: boolean) => {
    if (!from || !to || from === to || !byRef.has(from) || !byRef.has(to)) return;
    const k = `${from}>${to}`;
    if (seen.has(k)) return;
    seen.add(k);
    edges.push({ from, to, dashed });
  };
  for (const row of tree.rows) {
    for (const it of row.items) {
      if (row.gen >= 0) add(centers.get(row.gen - 1), it.post.ref, it.shownAbove);
      else add(it.post.ref, row.gen === -1 ? tree.focal : centers.get(row.gen + 1), it.shownAbove);
    }
  }

  const xs = [...nodes.map((n) => n.x), ...more.map((m) => m.x)];
  const ys = nodes.map((n) => n.y);
  const minX = Math.min(0, ...xs) - NODE_W / 2 - PAD;
  const maxX = Math.max(0, ...xs) + NODE_W / 2 + PAD;
  const minY = Math.min(0, ...ys) - NODE_H / 2 - PAD;
  const maxY = Math.max(0, ...ys) + NODE_H / 2 + PAD;
  return { nodes, edges, more, byRef, bounds: { x: minX, y: minY, w: maxX - minX, h: maxY - minY } };
}
