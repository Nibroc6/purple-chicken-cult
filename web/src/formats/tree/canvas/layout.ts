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
// already has. Lines are routed in route.ts: siblings hang from one shared
// line, and no two lines share a stretch. Relatives beyond the direct line (aunts
// and uncles, cousins, nieces and nephews …) hang from their own parents, a band
// below them, outside the direct posts on their parent's side, marked kin so
// the canvas can play them down. Owner: P1-W09.
import type { MediaVM, PostVM, Ref, TreeRowVM, TreeVM } from '../../../vm/types';
import { GAP_X, NODE_H, NODE_W, PAD, ROW_H } from './geometry';
import { routeEdges, type Point } from './route';

export { GAP_X, NODE_H, NODE_W, PAD, RADIUS, ROW_H } from './geometry';
export { PORT } from './route';

export interface CanvasNode {
  ref: Ref;
  gen: number;
  x: number; // center
  y: number; // center
  centered: boolean;
  focal: boolean;
  kin: boolean;
  tier: number;
  handle: string;
  name: string;
  excerpt: string;
  replies: number;
  media?: MediaVM; // the post's first picture, shown as a thumbnail
}

export type EdgeKind = 'reply' | 'also' | 'above' | 'kin';

export interface CanvasEdge {
  from: Ref; // upper end
  to: Ref; // lower end
  kind: EdgeKind;
  points: Point[]; // the line's corners, starting at from
  d: string; // its SVG path
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

// A post's slot on the canvas while laying out: its row, the band it ends up
// in, and its parents on the canvas (the row's parent first).
interface Slot {
  post: PostVM;
  gen: number;
  center: boolean;
  kin: boolean;
  band: number;
  moved: boolean;
  parents: Ref[];
}

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

  // Each post once, in its row's band: the centered post first, then the rest
  // in ranked order. A reply's parents are the row's centered post above plus
  // its other visible parents.
  const slots: Slot[] = [];
  const slotOf = new Map<Ref, Slot>();
  tree.rows.forEach((row, band) => {
    const center = centers.get(row.gen);
    const own = row.items.filter((i) => !i.shownAbove);
    const ordered = [...own.filter((i) => i.post.ref === center), ...own.filter((i) => i.post.ref !== center)];
    for (const it of ordered) {
      if (slotOf.has(it.post.ref)) continue;
      const above = centers.get(row.gen - 1);
      const parents = row.gen >= 0 ? [...(above ? [above] : []), ...it.alsoParents.filter((p) => p !== above)] : [];
      const sl: Slot = { post: it.post, gen: row.gen, center: it.post.ref === center, kin: false, band, moved: false, parents };
      slots.push(sl);
      slotOf.set(it.post.ref, sl);
    }
  });
  // Relatives, nearest first, each under a parent already on the map.
  for (const k of tree.kin ?? []) {
    if (slotOf.has(k.post.ref) || !slotOf.has(k.parent)) continue;
    const parents = [k.parent, ...k.alsoParents.filter((p) => p !== k.parent && slotOf.has(p))];
    const sl: Slot = { post: k.post, gen: k.gen, center: false, kin: true, band: 0, moved: false, parents };
    slots.push(sl);
    slotOf.set(k.post.ref, sl);
  }
  // A reply sits one band below its lowest parent (and its own replies below
  // it in turn), so it hangs from every parent it has on the canvas.
  for (let changed = true, pass = 0; changed && pass <= slots.length; pass++) {
    changed = false;
    for (const sl of slots) {
      for (const p of sl.parents) {
        const ps = slotOf.get(p);
        if (ps && ps.band >= sl.band) {
          sl.band = ps.band + 1;
          sl.moved = true;
          changed = true;
        }
      }
    }
  }

  const place = (sl: Slot, off: number) => {
    const p = sl.post;
    const n: CanvasNode = {
      ref: p.ref,
      gen: sl.gen,
      x: off * (NODE_W + GAP_X),
      y: sl.band * ROW_H,
      centered: sl.center,
      focal: p.ref === tree.focal,
      kin: sl.kin,
      tier: p.tier,
      handle: p.author.handle,
      name: p.author.displayName,
      excerpt: excerpt(p.body?.nodes),
      replies: p.replyCount,
      ...(p.media[0] ? { media: p.media[0] } : {}),
    };
    nodes.push(n);
    byRef.set(n.ref, n);
  };
  // Slots per band: the band's own posts in order, then the ones moved into it.
  const bands = new Map<number, Slot[]>();
  for (const sl of [...slots.filter((x) => !x.kin && !x.moved), ...slots.filter((x) => !x.kin && x.moved)]) {
    bands.set(sl.band, [...(bands.get(sl.band) ?? []), sl]);
  }
  const right = new Map<number, number>();
  const left = new Map<number, number>();
  for (const [band, list] of bands) {
    list.forEach((sl, rank) => {
      const off = slotOffset(rank);
      right.set(band, Math.max(right.get(band) ?? 0, off));
      left.set(band, Math.min(left.get(band) ?? 0, off));
      place(sl, off);
    });
  }
  tree.rows.forEach((row, band) => {
    if (row.more > 0) {
      const off = (right.get(band) ?? 0) + 1;
      right.set(band, off);
      more.push({
        gen: row.gen,
        center: row.center,
        ...(row.cursor ? { cursor: row.cursor } : {}),
        x: off * (NODE_W + GAP_X),
        y: band * ROW_H,
        count: row.more,
      });
    }
  });
  // Relatives go outside the band's direct posts, on their parent's side.
  for (const sl of slots.filter((x) => x.kin)) {
    const parent = sl.parents[0];
    const px = parent ? (byRef.get(parent)?.x ?? 0) : 0;
    const empty = !right.has(sl.band) && !left.has(sl.band);
    let off: number;
    if (empty) {
      off = Math.round(px / (NODE_W + GAP_X));
      right.set(sl.band, off);
      left.set(sl.band, off);
    } else if (px >= 0) {
      off = (right.get(sl.band) ?? 0) + 1;
      right.set(sl.band, off);
    } else {
      off = (left.get(sl.band) ?? 0) - 1;
      left.set(sl.band, off);
    }
    place(sl, off);
  }

  const seen = new Set<string>();
  const add = (from: Ref | undefined, to: Ref | undefined, kind: EdgeKind) => {
    if (!from || !to || from === to || !byRef.has(from) || !byRef.has(to)) return;
    const k = `${from}>${to}`;
    if (seen.has(k)) return;
    seen.add(k);
    edges.push({ from, to, kind, points: [], d: '' });
  };
  for (const sl of slots) {
    const ref = sl.post.ref;
    if (sl.kin) {
      sl.parents.forEach((p, i) => {
        add(p, ref, i === 0 ? 'kin' : 'also');
      });
    } else if (sl.gen >= 0) {
      sl.parents.forEach((p, i) => {
        add(p, ref, i === 0 ? 'reply' : 'also');
      });
    } else if (sl.gen === -1) {
      // The primary parent carries the focal row; the others join the focal post.
      add(ref, tree.focal, ref === centers.get(-1) ? 'reply' : 'also');
    } else {
      add(ref, centers.get(sl.gen + 1), 'reply');
    }
  }
  // A post shown again further down keeps one node; its second spot is a
  // dashed line to it, unless a parent line already joins the two.
  for (const row of tree.rows) {
    for (const it of row.items) {
      if (!it.shownAbove) continue;
      if (row.gen >= 0) add(centers.get(row.gen - 1), it.post.ref, 'above');
      else add(it.post.ref, row.gen === -1 ? tree.focal : centers.get(row.gen + 1), 'above');
    }
  }

  routeEdges(edges, byRef);

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
