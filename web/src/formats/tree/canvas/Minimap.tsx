// The desktop minimap (§10.1.4: "on desktop, zooming out shows a minimap"):
// the whole tree in miniature with the visible part outlined. Pressing or
// dragging in it moves the view there. It repeats what the canvas shows, so it
// is hidden from assistive technology. Owner: P1-W09.
import { useRef, type PointerEvent } from 'react';
import { capture } from './pointer';
import { NODE_H, NODE_W, type Bounds, type CanvasLayout } from './layout';

export function Minimap({ layout, view, onMove }: { layout: CanvasLayout; view: Bounds; onMove: (x: number, y: number) => void }) {
  const svg = useRef<SVGSVGElement>(null);
  const down = useRef(false);
  const b = layout.bounds;

  const move = (e: PointerEvent<SVGSVGElement>) => {
    const r = svg.current?.getBoundingClientRect();
    if (!r || r.width === 0 || r.height === 0) return;
    // preserveAspectRatio meet: the drawing is centered in the box.
    const s = Math.min(r.width / b.w, r.height / b.h);
    const ox = (r.width - b.w * s) / 2;
    const oy = (r.height - b.h * s) / 2;
    onMove(b.x + (e.clientX - r.left - ox) / s, b.y + (e.clientY - r.top - oy) / s);
  };

  return (
    <svg
      ref={svg}
      className="explorer-minimap"
      data-testid="explorer-minimap"
      aria-hidden="true"
      viewBox={`${String(b.x)} ${String(b.y)} ${String(b.w)} ${String(b.h)}`}
      preserveAspectRatio="xMidYMid meet"
      onPointerDown={(e) => {
        e.stopPropagation();
        down.current = true;
        capture(svg.current, e.pointerId);
        move(e);
      }}
      onPointerMove={(e) => {
        if (down.current) move(e);
      }}
      onPointerUp={() => {
        down.current = false;
      }}
      onPointerCancel={() => {
        down.current = false;
      }}
    >
      <rect className="explorer-minimap-bg" x={b.x} y={b.y} width={b.w} height={b.h} />
      {layout.nodes.map((n) => (
        <rect
          key={n.ref}
          className="explorer-minimap-node"
          data-focal={n.focal}
          x={n.x - NODE_W / 2}
          y={n.y - NODE_H / 2}
          width={NODE_W}
          height={NODE_H}
        />
      ))}
      <rect className="explorer-minimap-view" data-testid="explorer-minimap-view" x={view.x} y={view.y} width={view.w} height={view.h} />
    </svg>
  );
}
