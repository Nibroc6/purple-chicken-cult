// Node layout of the reply-tree canvas (pure; §10.1.4). One band per
// generation, top to bottom as the tree's rows come; in each band the row's
// centered post sits on the vertical axis and the others alternate right and
// left of it in ranked order, so the focal post and the line through its
// parents and replies run down the middle. Edges follow the rows' meaning: a
// row at or below the focal row holds replies to the centered post of the row
// above; a row above holds parents of the post below it (the focal post for
// "replying to", the centered parent further up). The focal post's other
// parents join it with an "also" edge. A post already shown higher up ("shown
// above", a diamond) gets no second node: its edge goes, dashed, to the node it
// already has. Edges are routed as family-tree elbows (down, across, down) so
// siblings hang from one shared line. Owner: P1-W09.
import type { Ref, TreeRowVM, TreeVM } from '../../../vm/types';

export const NODE_W = 208;
export const NODE_H = 96;
export const GAP_X = 32;
export const ROW_H = 168;
export const PAD = 64;
// RADIUS rounds an elbow's corners.
export const RADIUS = 10;

export interface CanvasNode {
  ref: Ref;
  gen: number;
  x: number; // center
  y: number; // center
  centered: boolean;
  focal: boolean;
  tier: number;
  handle: string;
  name: string;
  excerpt: string;
  replies: number;
}

export type EdgeKind = 'reply' | 'also' | 'above';

export interface CanvasEdge {
  from: Ref; // upper end
  to: Ref; // lower end
  kind: EdgeKind;
}

export interface CanvasMore {
  gen: number;
  center: Ref;
  cursor?: string;
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

const EXCERPT = 56;
// LINE is how many characters of an excerpt fit on one line of a card.
export const LINE = 27;

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
    const o = n as {
      v?: unknown;
      text?: unknown;
      c?: unknown;
      lines?: unknown;
      items?: unknown;
    };
    if (typeof o.v === 'string') out += o.v;
    else if (typeof o.text === 'string') out += o.text;
    for (const k of [o.c, o.lines, o.items]) if (Array.isArray(k)) k.forEach(walk);
    out += ' ';
  };
  (nodes ?? []).forEach(walk);
  const s = out.replace(/\s+/g, ' ').trim();
  return s.length > EXCERPT ? `${s.slice(0, EXCERPT - 1).trimEnd()}…` : s;
}

// lines wraps an excerpt into at most max lines of about width characters,
// breaking between words (a word longer than a line is cut), the last line
// ending in an ellipsis when text is left over.
export function lines(text: string, width = LINE, max = 2): string[] {
  const out: string[] = [];
  let rest = text.trim();
  while (rest !== '' && out.length < max) {
    if (rest.length <= width) {
      out.push(rest);
      rest = '';
      break;
    }
    let cut = rest.lastIndexOf(' ', width);
    if (cut <= 0) cut = width;
    out.push(rest.slice(0, cut).trimEnd());
    rest = rest.slice(cut).trimStart();
  }
  if (rest !== '' && out.length > 0) {
    const last = out[out.length - 1] ?? '';
    out[out.length - 1] = `${(last.length >= width ? last.slice(0, width - 1) : last).trimEnd()}…`;
  }
  return out;
}

// Centers gives the centered post of a row: the chosen one, else its first post.
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
        name: it.post.author.displayName,
        excerpt: excerpt(it.post.body?.nodes),
        replies: it.post.replyCount,
      };
      nodes.push(n);
      byRef.set(n.ref, n);
    });
    if (row.more > 0) {
      more.push({
        gen: row.gen,
        center: row.center,
        ...(row.cursor ? { cursor: row.cursor } : {}),
        x: (maxOffset + 1) * (NODE_W + GAP_X),
        y,
        count: row.more,
      });
    }
  });

  const seen = new Set<string>();
  const add = (from: Ref | undefined, to: Ref | undefined, kind: EdgeKind) => {
    if (!from || !to || from === to || !byRef.has(from) || !byRef.has(to)) return;
    const k = `${from}>${to}`;
    if (seen.has(k)) return;
    seen.add(k);
    edges.push({ from, to, kind });
  };
  for (const row of tree.rows) {
    for (const it of row.items) {
      if (it.shownAbove) {
        if (row.gen >= 0) add(centers.get(row.gen - 1), it.post.ref, 'above');
        else add(it.post.ref, row.gen === -1 ? tree.focal : centers.get(row.gen + 1), 'above');
      } else if (row.gen >= 0) {
        add(centers.get(row.gen - 1), it.post.ref, 'reply');
      } else if (row.gen === -1) {
        // The primary parent carries the focal row; the others join the focal post.
        add(it.post.ref, tree.focal, it.post.ref === centers.get(-1) ? 'reply' : 'also');
      } else {
        add(it.post.ref, centers.get(row.gen + 1), 'reply');
      }
    }
  }

  const xs = [...nodes.map((n) => n.x), ...more.map((m) => m.x)];
  const ys = nodes.map((n) => n.y);
  const minX = Math.min(0, ...xs) - NODE_W / 2 - PAD;
  const maxX = Math.max(0, ...xs) + NODE_W / 2 + PAD;
  const minY = Math.min(0, ...ys) - NODE_H / 2 - PAD;
  const maxY = Math.max(0, ...ys) + NODE_H / 2 + PAD;
  return {
    nodes,
    edges,
    more,
    byRef,
    bounds: { x: minX, y: minY, w: maxX - minX, h: maxY - minY },
  };
}

const n2 = (v: number) => String(Math.round(v * 10) / 10);

// elbow runs vertically from (x1, y1) to the line at my, across, and
// vertically on to (x2, y2), with rounded corners.
function elbow(x1: number, y1: number, x2: number, y2: number, my: number): string {
  const dx = x2 - x1;
  if (Math.abs(dx) < 1) return `M ${n2(x1)} ${n2(y1)} V ${n2(y2)}`;
  const v1 = Math.sign(my - y1) || 1;
  const v2 = Math.sign(y2 - my) || 1;
  const r = Math.min(RADIUS, Math.abs(dx) / 2, Math.abs(my - y1), Math.abs(y2 - my));
  const s = Math.sign(dx);
  return [
    `M ${n2(x1)} ${n2(y1)}`,
    `V ${n2(my - v1 * r)}`,
    `Q ${n2(x1)} ${n2(my)} ${n2(x1 + s * r)} ${n2(my)}`,
    `H ${n2(x2 - s * r)}`,
    `Q ${n2(x2)} ${n2(my)} ${n2(x2)} ${n2(my + v2 * r)}`,
    `V ${n2(y2)}`,
  ].join(' ');
}

// LOOP is how far below a row a line between two of its posts runs.
const LOOP = 20;

// route is the SVG path of an edge from a (the parent) to b (the reply). From
// a row to the one below, it runs from the bottom of a down to a line between
// the rows, across, and down to the top of b: replies share the line halfway
// down, so siblings hang from one line; "also" edges cross a third of the way
// down, apart from the focal row's siblings. A "shown above" edge whose reply
// sits in the same row loops under the row, and one whose reply sits higher
// up runs from the top of a up to the bottom of b.
export function route(a: { x: number; y: number }, b: { x: number; y: number }, kind: EdgeKind): string {
  if (b.y > a.y) {
    const y1 = a.y + NODE_H / 2;
    const y2 = b.y - NODE_H / 2;
    return elbow(a.x, y1, b.x, y2, y1 + (y2 - y1) * (kind === 'also' ? 0.35 : 0.5));
  }
  if (b.y === a.y) {
    const y = a.y + NODE_H / 2;
    return elbow(a.x, y, b.x, y, y + LOOP);
  }
  const y1 = a.y - NODE_H / 2;
  const y2 = b.y + NODE_H / 2;
  return elbow(a.x, y1, b.x, y2, (y1 + y2) / 2);
}
