// Line routing for the reply-tree canvas (pure; §10.1.4), after the orthogonal
// hyperedge routing of layered-graph tools (G. Sander, "Layout of directed
// hypergraphs with orthogonal hyperedges", and its use in Eclipse ELK Layered).
// Every line runs down (or across a loop) in vertical and horizontal pieces;
// the horizontal pieces live in the gaps between bands of cards and the
// vertical ones that pass bands live in lanes between columns. A parent's
// replies in the band below share one horizontal bus, as in a family tree;
// every other line has its own ports on its cards. Then each gap is solved on
// its own: pieces whose spans overlap must sit on different tracks, and which
// goes above which is chosen to cross the fewest vertical drops (a piece above
// another is crossed by the other's rising ends inside its span, and crosses
// the other with its own falling ends). Those choices form a graph; its cycles
// are broken greedily (Eades, Lin and Smyth) and the longest path from the top
// gives each piece its track. Lanes sharing a stretch of one column gap are
// spread across it the same way. Owner: P1-W09.
import type { Ref } from '../../../vm/types';
import { GAP_X, NODE_H, NODE_W, RADIUS, ROW_H } from './geometry';

export type Point = [number, number];

export interface Routable {
  from: Ref;
  to: Ref;
  kind: 'reply' | 'also' | 'above' | 'kin';
  d: string;
  points: Point[];
}

interface Card {
  x: number;
  y: number;
}

// PORT is the spacing of a card's extra ports, either side of its middle.
export const PORT = 18;
// MARGIN: pieces closer than this along a gap count as overlapping.
const MARGIN = 4;
// LANE is how far either side of a column gap's middle its lanes may run.
const LANE = GAP_X / 2 - 4;

const band = (c: Card) => Math.round(c.y / ROW_H);
const gapTop = (g: number) => g * ROW_H + NODE_H / 2;
const GAP = ROW_H - NODE_H;

// A horizontal piece in a gap: its span, and where lines leave it upward (to
// a card or lane above) and downward.
interface Piece {
  gap: number;
  x1: number;
  x2: number;
  ups: number[];
  downs: number[];
  y: number;
}

// A vertical piece in a column gap, from the gap g1 to the gap g2.
interface Lane {
  col: number;
  g1: number;
  g2: number;
  x: number;
}

function piece(gap: number, ups: number[], downs: number[]): Piece {
  const xs = [...ups, ...downs];
  return { gap, x1: Math.min(...xs), x2: Math.max(...xs), ups, downs, y: gapTop(gap) + GAP / 2 };
}

const overlaps = (a: Piece, b: Piece) => a.x1 < b.x2 + MARGIN && b.x1 < a.x2 + MARGIN;
const inside = (x: number, p: Piece) => x > p.x1 + 0.5 && x < p.x2 - 0.5;

// cost of putting a above b: b's rising ends cross a, a's falling ends cross
// b, and a falling end of a on the very spot b rises from would run along it.
const cost = (a: Piece, b: Piece) =>
  b.ups.filter((x) => inside(x, a)).length +
  a.downs.filter((x) => inside(x, b)).length +
  10 * a.downs.filter((x) => b.ups.some((u) => Math.abs(u - x) < 1)).length;

// tracks gives the pieces of one gap their heights.
function tracks(list: Piece[]): void {
  const flat = list.filter((p) => p.x2 - p.x1 >= 1);
  const n = flat.length;
  const above = flat.map(() => new Set<number>()); // i → pieces that go below i
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      const a = flat[i];
      const b = flat[j];
      if (!a || !b || !overlaps(a, b)) continue;
      const ab = cost(a, b);
      const ba = cost(b, a);
      if (ab < ba || (ab === ba && a.x1 <= b.x1)) above[i]?.add(j);
      else above[j]?.add(i);
    }
  }
  // Break cycles: an order with the fewest edges pointing backwards.
  const left: number[] = [];
  const right: number[] = [];
  const live = new Set(flat.map((_, i) => i));
  const outs = (i: number) => [...(above[i] ?? [])].filter((j) => live.has(j)).length;
  const ins = (i: number) => [...live].filter((j) => above[j]?.has(i)).length;
  while (live.size > 0) {
    let moved = true;
    while (moved) {
      moved = false;
      for (const i of [...live]) {
        if (outs(i) === 0) {
          right.unshift(i);
          live.delete(i);
          moved = true;
        } else if (ins(i) === 0) {
          left.push(i);
          live.delete(i);
          moved = true;
        }
      }
    }
    if (live.size === 0) break;
    let best = -1;
    let score = -Infinity;
    for (const i of live) {
      const s = outs(i) - ins(i);
      if (s > score) [best, score] = [i, s];
    }
    left.push(best);
    live.delete(best);
  }
  const order = [...left, ...right];
  const pos = new Map(order.map((i, k) => [i, k]));
  const level = new Map<number, number>();
  for (const i of order) {
    let t = 0;
    for (const j of order) {
      if ((pos.get(j) ?? 0) >= (pos.get(i) ?? 0)) break;
      // A choice the cycle breaking turned round still keeps the two apart.
      if (above[j]?.has(i) || above[i]?.has(j)) t = Math.max(t, (level.get(j) ?? 0) + 1);
    }
    level.set(i, t);
  }
  const count = Math.max(0, ...level.values()) + 1;
  flat.forEach((p, i) => {
    p.y = gapTop(p.gap) + (GAP * ((level.get(i) ?? 0) + 1)) / (count + 1);
  });
}

// lanes spreads the lanes of one column gap that share a stretch of it.
function lanes(list: Lane[]): void {
  const sorted = [...list].sort((a, b) => a.g1 - b.g1 || a.g2 - b.g2);
  const ends: number[] = []; // per slot, the last gap it is busy to
  const slot = new Map<Lane, number>();
  for (const l of sorted) {
    let s = ends.findIndex((e) => e < l.g1);
    if (s < 0) s = ends.length;
    ends[s] = l.g2;
    slot.set(l, s);
  }
  const n = ends.length;
  for (const l of list) l.x = l.col + (n === 1 ? 0 : -LANE + (2 * LANE * (slot.get(l) ?? 0)) / (n - 1));
}

// clean drops repeated points and points in the middle of a straight run.
function clean(pts: Point[]): Point[] {
  const out: Point[] = [];
  for (const p of pts) {
    const last = out[out.length - 1];
    if (last && Math.abs(last[0] - p[0]) < 0.5 && Math.abs(last[1] - p[1]) < 0.5) continue;
    const prev = out[out.length - 2];
    if (last && prev) {
      const sameX = Math.abs(prev[0] - last[0]) < 0.5 && Math.abs(last[0] - p[0]) < 0.5;
      const sameY = Math.abs(prev[1] - last[1]) < 0.5 && Math.abs(last[1] - p[1]) < 0.5;
      if (sameX || sameY) out.pop();
    }
    out.push(p);
  }
  return out;
}

const n2 = (v: number) => String(Math.round(v * 10) / 10);

// path joins points by straight lines with rounded corners.
export function path(pts: Point[]): string {
  const out: string[] = [];
  pts.forEach(([x, y], i) => {
    const prev = pts[i - 1];
    const next = pts[i + 1];
    if (!prev) {
      out.push(`M ${n2(x)} ${n2(y)}`);
      return;
    }
    if (!next) {
      out.push(`L ${n2(x)} ${n2(y)}`);
      return;
    }
    const r = Math.min(RADIUS, Math.hypot(x - prev[0], y - prev[1]) / 2, Math.hypot(next[0] - x, next[1] - y) / 2);
    const back = (p: Point): Point => [x + Math.sign(p[0] - x) * r, y + Math.sign(p[1] - y) * r];
    const [bx, by] = back(prev);
    const [fx, fy] = back(next);
    out.push(`L ${n2(bx)} ${n2(by)}`, `Q ${n2(x)} ${n2(y)} ${n2(fx)} ${n2(fy)}`);
  });
  return out.join(' ');
}

// routeEdges sets every edge's points and SVG path.
export function routeEdges(edges: Routable[], byRef: Map<Ref, Card>): void {
  type Ends = { e: Routable; up: Card; low: Card; ub: number; lb: number };
  const ends: Ends[] = [];
  for (const e of edges) {
    const a = byRef.get(e.from);
    const b = byRef.get(e.to);
    if (!a || !b) continue;
    const [up, low] = b.y >= a.y ? [a, b] : [b, a];
    ends.push({ e, up, low, ub: band(up), lb: band(low) });
  }
  // laneCol is the column gap a line passing bands runs down: beside its
  // lower card, on the side of its upper one.
  const laneCol = (x: Ends) => x.low.x + (Math.sign(x.up.x - x.low.x) || 1) * ((NODE_W + GAP_X) / 2);
  const trunk = (x: Ends) => (x.e.kind === 'reply' || x.e.kind === 'kin') && x.lb === x.ub + 1 && x.up === byRef.get(x.e.from);

  // Ports: a card's extra lines leave its bottom (or reach its top) on the
  // side they head for, nearest target innermost, PORT apart. The cards above
  // and below one gap in one column count their ports together, so a line
  // leaving one never meets a line reaching the other.
  const port = new Map<string, number>();
  const used = new Map<string, number>();
  const take = (card: Card, edge: 'top' | 'bottom', toward: number, key: string, dist: number) => {
    const side = Math.sign(toward - card.x) || 1;
    const gap = band(card) - (edge === 'top' ? 1 : 0);
    return { k: `${String(card.x)}:${String(gap)}:${String(side)}`, side, key, dist };
  };
  const asks: ReturnType<typeof take>[] = [];
  // Where parents' own lines leave their cards (column, gap).
  const stubs = new Set(ends.filter(trunk).map((x) => `${String(x.up.x)}:${String(x.ub)}`));
  for (const x of ends) {
    if (trunk(x)) {
      // A reply under another parent's card would drop where that card's own
      // line leaves it: it arrives off-centre instead.
      const k = `${String(x.low.x)}:${String(x.ub)}`;
      if (stubs.has(k) && x.low.x !== x.up.x) asks.push(take(x.low, 'top', x.up.x, `${x.e.from}>${x.e.to}:low`, Math.abs(x.low.x - x.up.x)));
      continue;
    }
    const loop = x.lb === x.ub;
    asks.push(take(x.up, 'bottom', x.low.x, `${x.e.from}>${x.e.to}:up`, Math.abs(x.low.x - x.up.x)));
    const lane = x.lb - x.ub >= 2 ? laneCol(x) : x.up.x;
    asks.push(take(x.low, loop ? 'bottom' : 'top', lane, `${x.e.from}>${x.e.to}:low`, Math.abs(x.low.x - x.up.x)));
  }
  const count = new Map<string, number>();
  for (const a of asks) count.set(a.k, (count.get(a.k) ?? 0) + 1);
  for (const a of [...asks].sort((p, q) => p.dist - q.dist)) {
    const n = (used.get(a.k) ?? 0) + 1;
    used.set(a.k, n);
    // Many lines on one side squeeze together to stay on the card.
    const step = Math.min(PORT, (NODE_W / 2 - RADIUS) / (count.get(a.k) ?? 1));
    port.set(a.key, a.side * step * n);
  }

  // Lanes for lines that pass a band.
  const laneOf = new Map<Routable, Lane>();
  const byCol = new Map<number, Lane[]>();
  for (const x of ends) {
    if (x.lb - x.ub < 2) continue;
    const col = laneCol(x);
    const l: Lane = { col, g1: x.ub, g2: x.lb - 1, x: col };
    laneOf.set(x.e, l);
    byCol.set(col, [...(byCol.get(col) ?? []), l]);
  }
  for (const list of byCol.values()) lanes(list);

  // Horizontal pieces, then their tracks gap by gap.
  const pieces: Piece[] = [];
  const add = (p: Piece) => {
    pieces.push(p);
    return p;
  };
  const buses = new Map<string, Piece>();
  const plan = new Map<Routable, () => Point[]>();
  for (const x of ends) {
    const { e, up, low, ub, lb } = x;
    const y1 = up.y + NODE_H / 2;
    const y2 = low.y - NODE_H / 2;
    if (trunk(x)) {
      const k = `${e.from}:${String(ub)}`;
      let bus = buses.get(k);
      if (!bus) {
        bus = add(piece(ub, [up.x], []));
        buses.set(k, bus);
      }
      const lx = low.x + (port.get(`${e.from}>${e.to}:low`) ?? 0);
      bus.downs.push(lx);
      bus.x1 = Math.min(bus.x1, lx);
      bus.x2 = Math.max(bus.x2, lx);
      const b = bus;
      plan.set(e, () => [
        [up.x, y1],
        [up.x, b.y],
        [lx, b.y],
        [lx, y2],
      ]);
      continue;
    }
    const ux = up.x + (port.get(`${e.from}>${e.to}:up`) ?? 0);
    const lx = low.x + (port.get(`${e.from}>${e.to}:low`) ?? 0);
    if (lb === ub) {
      const p = add(piece(ub, [ux, lx], []));
      plan.set(e, () => [
        [ux, y1],
        [ux, p.y],
        [lx, p.y],
        [lx, low.y + NODE_H / 2],
      ]);
    } else if (lb === ub + 1) {
      const p = add(piece(ub, [ux], [lx]));
      plan.set(e, () => [
        [ux, y1],
        [ux, p.y],
        [lx, p.y],
        [lx, y2],
      ]);
    } else {
      const l = laneOf.get(e);
      const lane = l?.x ?? lx;
      const p1 = add(piece(ub, [ux], [lane]));
      const p2 = add(piece(lb - 1, [lane], [lx]));
      plan.set(e, () => [
        [ux, y1],
        [ux, p1.y],
        [lane, p1.y],
        [lane, p2.y],
        [lx, p2.y],
        [lx, y2],
      ]);
    }
  }
  const byGap = new Map<number, Piece[]>();
  for (const p of pieces) byGap.set(p.gap, [...(byGap.get(p.gap) ?? []), p]);
  for (const list of byGap.values()) tracks(list);

  for (const x of ends) {
    const pts = clean(plan.get(x.e)?.() ?? []);
    // Lines are drawn from the parent: keep the edge's own direction.
    if (x.up !== byRef.get(x.e.from)) pts.reverse();
    x.e.points = pts;
    x.e.d = path(pts);
  }
}
