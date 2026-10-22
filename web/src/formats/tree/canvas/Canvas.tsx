// The reply-tree canvas (§10.1.4): every post of the tree as a card on one
// pannable, zoomable map, joined by family-tree lines, the focal post
// highlighted in the middle. Drag (or the arrow keys) pans; the wheel, a pinch
// (or + / −) zooms around the pointer; 0 or the fit button shows the whole
// tree. Clicking a post (or Tab to it and Enter) brings it into focus: the
// tree reopens around it. "+N more" loads the rest of a generation. On
// desktop, zooming out shows a minimap of the whole tree with the visible part
// outlined; dragging in it moves the view. Owner: P1-W09.
import { useCallback, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Icon } from '../../../lib/Icon';
import { lines, NODE_H, NODE_W, route, type Bounds, type CanvasLayout, type CanvasMore, type CanvasNode } from './layout';
import { Minimap } from './Minimap';
import { capture } from './pointer';
import './canvas.css';

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
  return {
    cx: b.x + b.w / 2,
    cy: b.y + b.h / 2,
    k: clampK(Math.min(1, s.w / b.w, s.h / b.h)),
  };
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

// initial is the first letter of a name, for the card's avatar.
function initial(name: string): string {
  return Array.from(name.trim())[0]?.toUpperCase() ?? '?';
}

// activate runs fn on Enter or Space (an SVG group acting as a button).
function activate(fn: () => void) {
  return (e: KeyboardEvent<SVGGElement>) => {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    e.preventDefault();
    e.stopPropagation();
    fn();
  };
}

function Node({ n, clip, onPick }: { n: CanvasNode; clip: string; onPick: (n: CanvasNode, key: boolean) => void }) {
  const { t } = useTranslation('explorer');
  const label = n.focal
    ? t('canvas.focal', { handle: n.handle, text: n.excerpt })
    : t('canvas.open', { handle: n.handle, text: n.excerpt });
  return (
    <g
      className="explorer-node"
      data-testid="explorer-node"
      data-ref={n.ref}
      data-gen={n.gen}
      data-tier={n.tier}
      data-centered={n.centered}
      data-focal={n.focal}
      role="button"
      tabIndex={0}
      aria-label={label}
      aria-current={n.focal ? 'true' : undefined}
      style={{
        transform: `translate(${String(n.x - NODE_W / 2)}px, ${String(n.y - NODE_H / 2)}px)`,
      }}
      onClick={() => {
        onPick(n, false);
      }}
      onKeyDown={activate(() => {
        onPick(n, true);
      })}
    >
      <rect className="explorer-node-box" width={NODE_W} height={NODE_H} />
      <g clipPath={`url(#${clip})`} aria-hidden="true">
        <circle className="explorer-node-avatar" cx={26} cy={26} r={14} />
        <text className="explorer-node-initial" x={26} y={31} textAnchor="middle" data-user-content="">
          {initial(n.name || n.handle)}
        </text>
        <text className="explorer-node-name" x={48} y={22} data-user-content="">
          {n.name || t('canvas.node', { handle: n.handle })}
        </text>
        <text className="explorer-node-handle" x={48} y={38}>
          {t('canvas.node', { handle: n.handle })}
        </text>
        {n.excerpt ? (
          <text className="explorer-node-text" y={66} data-user-content="">
            {/* A space ends each wrapped line so the text still reads as one. */}
            {lines(n.excerpt).map((l, i, all) => (
              <tspan key={i} x={12} dy={i === 0 ? 0 : 18}>
                {i < all.length - 1 ? `${l} ` : l}
              </tspan>
            ))}
          </text>
        ) : null}
      </g>
      {n.replies > 0 ? (
        <text className="explorer-node-replies" x={NODE_W - 10} y={22} textAnchor="end" aria-hidden="true">
          {t('canvas.replies', { count: n.replies })}
        </text>
      ) : null}
    </g>
  );
}

function More({ m, onMore }: { m: CanvasMore; onMore: (m: CanvasMore, key: boolean) => void }) {
  const { t } = useTranslation('explorer');
  const can = m.cursor !== undefined;
  const run = (key: boolean) => () => {
    if (can) onMore(m, key);
  };
  return (
    <g
      className="explorer-more"
      data-testid="explorer-more"
      data-gen={m.gen}
      data-count={m.count}
      role={can ? 'button' : undefined}
      tabIndex={can ? 0 : undefined}
      aria-label={t('canvas.moreLabel', { count: m.count })}
      style={{ transform: `translate(${String(m.x)}px, ${String(m.y)}px)` }}
      onClick={run(false)}
      onKeyDown={activate(run(true))}
    >
      <rect className="explorer-more-box" x={-36} y={-16} width={72} height={32} rx={16} />
      <text x={0} y={5} textAnchor="middle" aria-hidden="true">
        {t('canvas.more', { count: m.count })}
      </text>
    </g>
  );
}

export function Canvas({
  layout,
  focal,
  onFocus,
  onMore,
  tools,
}: {
  layout: CanvasLayout;
  focal: CanvasNode | undefined;
  onFocus: (n: CanvasNode) => void;
  onMore: (m: CanvasMore) => void;
  tools?: ReactNode;
}) {
  const { t } = useTranslation('explorer');
  const uid = useId().replace(/:/g, '');
  const clip = `${uid}-clip`;
  const hint = `${uid}-hint`;
  const box = useRef<HTMLDivElement>(null);
  const svg = useRef<SVGSVGElement>(null);
  const size = useSize(box);
  const desktop = useDesktop();
  const [view, setView] = useState<View>(() => ({
    cx: focal?.x ?? 0,
    cy: focal?.y ?? 0,
    k: 1,
  }));
  // Pointers down on the canvas: one drags, two pinch.
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const moved = useRef(false);
  const vb = viewBox(view, size);
  const { bounds } = layout;

  // A new focal post (the tree reopened around another post) recenters on it.
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
      return {
        x: vb.x + ((clientX - r.left) / r.width) * vb.w,
        y: vb.y + ((clientY - r.top) / r.height) * vb.h,
      };
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
  const showFocal = () => {
    if (focal) setView((v) => ({ ...v, cx: focal.x, cy: focal.y }));
  };

  const onKeyDown = (e: KeyboardEvent<SVGSVGElement>) => {
    const d = PAN / view.k;
    const moves: Record<string, [number, number]> = {
      ArrowLeft: [-d, 0],
      ArrowRight: [d, 0],
      ArrowUp: [0, -d],
      ArrowDown: [0, d],
    };
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
    if (e.pointerType === 'mouse' && e.button !== 0) return;
    // A first finger (or the mouse) starts afresh: a pointer whose up event was
    // missed never turns the next drag into a pinch.
    if (e.isPrimary) {
      pointers.current.clear();
      moved.current = false;
    }
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
  };
  const onPointerMove = (e: PointerEvent<SVGSVGElement>) => {
    const ps = pointers.current;
    const prev = ps.get(e.pointerId);
    if (!prev) return;
    const r = svg.current?.getBoundingClientRect();
    const scale = r && r.width > 0 ? vb.w / r.width : 1 / view.k;
    if (ps.size >= 2) {
      // Pinch: zoom by the change in distance around the midpoint, and pan
      // with the midpoint.
      const [a, b] = [...ps.entries()].slice(0, 2);
      if (!a || !b) return;
      const other = a[0] === e.pointerId ? b[1] : a[1];
      const before = Math.hypot(prev.x - other.x, prev.y - other.y);
      const after = Math.hypot(e.clientX - other.x, e.clientY - other.y);
      const mid = toCanvas((e.clientX + other.x) / 2, (e.clientY + other.y) / 2);
      ps.set(e.pointerId, { x: e.clientX, y: e.clientY });
      moved.current = true;
      if (before > 0) setView((v) => zoomAt(v, after / before, mid.x, mid.y));
      return;
    }
    const dx = e.clientX - prev.x;
    const dy = e.clientY - prev.y;
    if (!moved.current && Math.hypot(dx, dy) < DRAG_SLOP) return;
    if (!moved.current) capture(svg.current, e.pointerId);
    moved.current = true;
    ps.set(e.pointerId, { x: e.clientX, y: e.clientY });
    setView((v) => ({ ...v, cx: v.cx - dx * scale, cy: v.cy - dy * scale }));
  };
  const onPointerUp = (e: PointerEvent<SVGSVGElement>) => {
    pointers.current.delete(e.pointerId);
    // The click that ends a drag comes right after; later clicks count again.
    if (moved.current && pointers.current.size === 0) {
      setTimeout(() => {
        moved.current = false;
      }, 0);
    }
  };

  // The end of a drag or pinch is not a click (a key press always is).
  const pick = (n: CanvasNode, key: boolean) => {
    if (moved.current && !key) return;
    if (n.focal) showFocal();
    else onFocus(n);
  };
  const more = (m: CanvasMore, key: boolean) => {
    if (!moved.current || key) onMore(m);
  };

  const zoomedOut = view.k < 1;
  const edges = useMemo(
    () =>
      layout.edges.map((e) => {
        const a = layout.byRef.get(e.from);
        const b = layout.byRef.get(e.to);
        if (!a || !b) return null;
        return {
          key: `${e.from}>${e.to}`,
          d: route(a, b, e.kind),
          kind: e.kind,
          focal: a.focal || b.focal,
        };
      }),
    [layout],
  );

  return (
    <div className="explorer-canvas" data-testid="explorer-canvas" data-zoom={view.k.toFixed(2)}>
      <div className="explorer-toolbar tree-overlay" role="group" aria-label={t('canvas.tools')}>
        <button
          type="button"
          data-testid="explorer-zoom-out"
          aria-label={t('canvas.zoomOut')}
          disabled={view.k <= MIN_ZOOM}
          onClick={() => {
            zoomBy(1 / STEP);
          }}
        >
          <Icon name="zoom-out" />
        </button>
        <span className="explorer-zoom muted" data-testid="explorer-zoom">
          {t('canvas.zoom', { percent: Math.round(view.k * 100) })}
        </span>
        <button
          type="button"
          data-testid="explorer-zoom-in"
          aria-label={t('canvas.zoomIn')}
          disabled={view.k >= MAX_ZOOM}
          onClick={() => {
            zoomBy(STEP);
          }}
        >
          <Icon name="zoom-in" />
        </button>
        <button type="button" data-testid="explorer-fit" onClick={showAll}>
          {t('canvas.fit')}
        </button>
        <button type="button" data-testid="explorer-recenter" onClick={showFocal}>
          {t('canvas.recenter')}
        </button>
        {tools}
      </div>
      <div className="explorer-stage" ref={box}>
        <svg
          ref={svg}
          className="explorer-svg"
          data-testid="explorer-svg"
          role="group"
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
              <rect x={4} y={0} width={NODE_W - 12} height={NODE_H} />
            </clipPath>
          </defs>
          <g className="explorer-edges" aria-hidden="true">
            {edges.map((e) => (e ? <path key={e.key} d={e.d} className="explorer-edge" data-kind={e.kind} data-focal={e.focal} /> : null))}
          </g>
          {layout.nodes.map((n) => (
            <Node key={n.ref} n={n} clip={clip} onPick={pick} />
          ))}
          {layout.more.map((m) => (
            <More key={`more-${String(m.gen)}-${m.center}`} m={m} onMore={more} />
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
      <p id={hint} className="explorer-hint visually-hidden">
        {t('canvas.hint')}
      </p>
    </div>
  );
}
