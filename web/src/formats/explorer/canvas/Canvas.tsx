// The Explorer canvas (§15.1): the family tree as a pan-and-zoom node map.
// Drag (or the arrow keys) pans, the wheel (or + / −) zooms around the
// pointer, 0 or the fit button shows the whole tree. Clicking a node selects
// it (the outline mirror below scrolls to it). On desktop, zooming out shows a
// minimap of the whole tree with the visible part outlined; dragging in it
// moves the view (§10.1.4). The map is a picture of the outline: keyboard and
// screen-reader users read and act in the outline, which has every post with
// all its integrity elements. Owner: P1-W09.
import { useCallback, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react';
import { useTranslation } from 'react-i18next';
import type { TreeRowVM } from '../../../vm/types';
import { NODE_H, NODE_W, ROW_H, type Bounds, type CanvasLayout, type CanvasNode } from './layout';
import { Minimap } from './Minimap';
import { capture } from './pointer';

export const MIN_ZOOM = 0.25;
export const MAX_ZOOM = 2;
const STEP = 1.25;
const PAN = 48;
const DRAG_SLOP = 4;

export interface View {
  cx: number;
  cy: number;
  k: number;
}

export interface Size {
  w: number;
  h: number;
}

const clampK = (k: number) => Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, k));

// viewBox is the canvas rectangle the SVG shows for a view.
export function viewBox(v: View, s: Size): Bounds {
  const w = s.w / v.k;
  const h = s.h / v.k;
  return { x: v.cx - w / 2, y: v.cy - h / 2, w, h };
}

// fit shows the whole tree, never larger than 1:1.
export function fit(b: Bounds, s: Size): View {
  return { cx: b.x + b.w / 2, cy: b.y + b.h / 2, k: clampK(Math.min(1, s.w / b.w, s.h / b.h)) };
}

// zoomAt scales by factor keeping the canvas point (px, py) under the pointer.
export function zoomAt(v: View, factor: number, px: number, py: number): View {
  const k = clampK(v.k * factor);
  const r = v.k / k;
  return { k, cx: px - (px - v.cx) * r, cy: py - (py - v.cy) * r };
}

// useDesktop is true on wide screens (the minimap is a desktop affordance).
function useDesktop(): boolean {
  const q = '(min-width: 64rem)';
  const [on, setOn] = useState(() => typeof window.matchMedia === 'function' && window.matchMedia(q).matches);
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return;
    const m = window.matchMedia(q);
    const onChange = () => {
      setOn(m.matches);
    };
    m.addEventListener('change', onChange);
    return () => {
      m.removeEventListener('change', onChange);
    };
  }, []);
  return on;
}

function useSize(el: React.RefObject<HTMLElement | null>): Size {
  const [size, setSize] = useState<Size>({ w: 640, h: 420 });
  useEffect(() => {
    const node = el.current;
    if (!node || typeof ResizeObserver !== 'function') return;
    const ro = new ResizeObserver(([e]) => {
      const r = e?.contentRect;
      if (r && r.width > 0 && r.height > 0) setSize({ w: r.width, h: r.height });
    });
    ro.observe(node);
    return () => {
      ro.disconnect();
    };
  }, [el]);
  return size;
}

// i18n-dynamic: tree:row.
function BandLabels({ rows, x }: { rows: readonly TreeRowVM[]; x: number }) {
  const { t } = useTranslation('tree');
  return (
    <g className="explorer-bands">
      {rows.map((row, i) => (
        <text key={`${String(row.gen)}-${row.center}`} className="explorer-band" x={x + 8} y={i * ROW_H - NODE_H / 2 - 8}>
          {t(`row.${row.label}` as 'row.replies', { count: Math.abs(row.gen) })}
        </text>
      ))}
    </g>
  );
}

function Node({ n, selected, clip, onPick }: { n: CanvasNode; selected: boolean; clip: string; onPick: (n: CanvasNode) => void }) {
  const { t } = useTranslation('explorer');
  return (
    <g
      className="explorer-node"
      data-testid="explorer-node"
      data-ref={n.ref}
      data-gen={n.gen}
      data-tier={n.tier}
      data-centered={n.centered}
      data-focal={n.focal}
      data-selected={selected}
      transform={`translate(${String(n.x - NODE_W / 2)} ${String(n.y - NODE_H / 2)})`}
      onClick={() => {
        onPick(n);
      }}
    >
      <rect className="explorer-node-box" width={NODE_W} height={NODE_H} />
      <g clipPath={`url(#${clip})`}>
        <text className="explorer-node-handle" x={10} y={22}>
          {t('canvas.node', { handle: n.handle })}
        </text>
        {n.excerpt ? (
          <text className="explorer-node-text" x={10} y={42} data-user-content="">
            {n.excerpt}
          </text>
        ) : null}
      </g>
    </g>
  );
}

export function Canvas({
  layout,
  rows,
  focal,
  selected,
  onSelect,
}: {
  layout: CanvasLayout;
  rows: readonly TreeRowVM[];
  focal: CanvasNode | undefined;
  selected: string | undefined;
  onSelect: (n: CanvasNode) => void;
}) {
  const { t } = useTranslation('explorer');
  const uid = useId().replace(/:/g, '');
  const clip = `${uid}-clip`;
  const hint = `${uid}-hint`;
  const box = useRef<HTMLDivElement>(null);
  const svg = useRef<SVGSVGElement>(null);
  const size = useSize(box);
  const desktop = useDesktop();
  const [view, setView] = useState<View>(() => ({ cx: focal?.x ?? 0, cy: focal?.y ?? 0, k: 1 }));
  const drag = useRef<{ id: number; x: number; y: number; moved: boolean } | null>(null);
  const dragged = useRef(false);
  const vb = viewBox(view, size);
  const { bounds } = layout;

  // A new focal post (another tree, or rows reloaded around it) recenters on it.
  const [onFocal, setOnFocal] = useState(focal?.ref);
  if (focal && focal.ref !== onFocal) {
    setOnFocal(focal.ref);
    setView((v) => ({ ...v, cx: focal.x, cy: focal.y }));
  }

  // Canvas coordinates of a client point.
  const toCanvas = useCallback(
    (clientX: number, clientY: number) => {
      const r = svg.current?.getBoundingClientRect();
      if (!r || r.width === 0) return { x: view.cx, y: view.cy };
      return { x: vb.x + ((clientX - r.left) / r.width) * vb.w, y: vb.y + ((clientY - r.top) / r.height) * vb.h };
    },
    [vb.x, vb.y, vb.w, vb.h, view.cx, view.cy],
  );

  // The wheel zooms the canvas, not the page (a non-passive listener).
  useEffect(() => {
    const el = svg.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const p = toCanvas(e.clientX, e.clientY);
      setView((v) => zoomAt(v, Math.exp(-e.deltaY * 0.0015), p.x, p.y));
    };
    el.addEventListener('wheel', onWheel, { passive: false });
    return () => {
      el.removeEventListener('wheel', onWheel);
    };
  }, [toCanvas]);

  const zoomBy = (f: number) => {
    setView((v) => zoomAt(v, f, v.cx, v.cy));
  };
  const showAll = () => {
    setView(fit(bounds, size));
  };

  const onKeyDown = (e: KeyboardEvent<SVGSVGElement>) => {
    const d = PAN / view.k;
    const moves: Record<string, [number, number]> = { ArrowLeft: [-d, 0], ArrowRight: [d, 0], ArrowUp: [0, -d], ArrowDown: [0, d] };
    const m = moves[e.key];
    if (m) {
      setView((v) => ({ ...v, cx: v.cx + m[0], cy: v.cy + m[1] }));
    } else if (e.key === '+' || e.key === '=') zoomBy(STEP);
    else if (e.key === '-' || e.key === '_') zoomBy(1 / STEP);
    else if (e.key === '0') showAll();
    else return;
    e.preventDefault();
  };

  const onPointerDown = (e: PointerEvent<SVGSVGElement>) => {
    if (e.button !== 0) return;
    drag.current = { id: e.pointerId, x: e.clientX, y: e.clientY, moved: false };
    dragged.current = false;
  };
  const onPointerMove = (e: PointerEvent<SVGSVGElement>) => {
    const d = drag.current;
    if (!d || d.id !== e.pointerId) return;
    const dx = e.clientX - d.x;
    const dy = e.clientY - d.y;
    if (!d.moved && Math.hypot(dx, dy) < DRAG_SLOP) return;
    if (!d.moved) capture(svg.current, e.pointerId);
    d.moved = true;
    dragged.current = true;
    d.x = e.clientX;
    d.y = e.clientY;
    const r = svg.current?.getBoundingClientRect();
    const scale = r && r.width > 0 ? vb.w / r.width : 1 / view.k;
    setView((v) => ({ ...v, cx: v.cx - dx * scale, cy: v.cy - dy * scale }));
  };
  const onPointerUp = (e: PointerEvent<SVGSVGElement>) => {
    if (drag.current?.id === e.pointerId) drag.current = null;
  };

  const pick = (n: CanvasNode) => {
    if (dragged.current) return; // the end of a drag is not a click
    onSelect(n);
  };

  const zoomedOut = view.k < 1;
  const edges = useMemo(
    () =>
      layout.edges.map((e) => {
        const a = layout.byRef.get(e.from);
        const b = layout.byRef.get(e.to);
        if (!a || !b) return null;
        const y1 = a.y + NODE_H / 2;
        const y2 = b.y - NODE_H / 2;
        const my = (y1 + y2) / 2;
        return { key: `${e.from}>${e.to}`, d: `M ${String(a.x)} ${String(y1)} C ${String(a.x)} ${String(my)}, ${String(b.x)} ${String(my)}, ${String(b.x)} ${String(y2)}`, dashed: e.dashed };
      }),
    [layout],
  );

  return (
    <div className="explorer-canvas" data-testid="explorer-canvas" data-zoom={view.k.toFixed(2)}>
      <div className="explorer-toolbar" role="group" aria-label={t('canvas.tools')}>
        <button type="button" data-testid="explorer-zoom-out" aria-label={t('canvas.zoomOut')} disabled={view.k <= MIN_ZOOM} onClick={() => {
            zoomBy(1 / STEP);
          }}>
          {t('canvas.zoomOutShort')}
        </button>
        <span className="explorer-zoom muted" data-testid="explorer-zoom">
          {t('canvas.zoom', { percent: Math.round(view.k * 100) })}
        </span>
        <button type="button" data-testid="explorer-zoom-in" aria-label={t('canvas.zoomIn')} disabled={view.k >= MAX_ZOOM} onClick={() => {
            zoomBy(STEP);
          }}>
          {t('canvas.zoomInShort')}
        </button>
        <button type="button" data-testid="explorer-fit" onClick={showAll}>
          {t('canvas.fit')}
        </button>
      </div>
      <div className="explorer-stage" ref={box}>
        <svg
          ref={svg}
          className="explorer-svg"
          data-testid="explorer-svg"
          role="img"
          tabIndex={0}
          aria-label={t('canvas.label')}
          aria-describedby={hint}
          viewBox={`${String(vb.x)} ${String(vb.y)} ${String(vb.w)} ${String(vb.h)}`}
          preserveAspectRatio="xMidYMid meet"
          onKeyDown={onKeyDown}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onPointerCancel={onPointerUp}
        >
          <defs>
            <clipPath id={clip} clipPathUnits="userSpaceOnUse">
              <rect x={4} y={0} width={NODE_W - 10} height={NODE_H} />
            </clipPath>
          </defs>
          <BandLabels rows={rows} x={bounds.x} />
          <g className="explorer-edges">
            {edges.map((e) => (e ? <path key={e.key} d={e.d} className="explorer-edge" data-dashed={e.dashed} /> : null))}
          </g>
          {layout.nodes.map((n) => (
            <Node key={n.ref} n={n} selected={n.ref === selected} clip={clip} onPick={pick} />
          ))}
          {layout.more.map((m) => (
            <text key={`more-${String(m.gen)}`} className="explorer-more" data-testid="explorer-more" x={m.x} y={m.y + 4} textAnchor="middle">
              {t('canvas.more', { count: m.count })}
            </text>
          ))}
        </svg>
        {desktop && zoomedOut ? (
          <Minimap
            layout={layout}
            view={vb}
            onMove={(x, y) => {
              setView((v) => ({ ...v, cx: x, cy: y }));
            }}
          />
        ) : null}
      </div>
      <p id={hint} className="muted explorer-hint">
        {t('canvas.hint')}
      </p>
    </div>
  );
}
