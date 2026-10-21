// Explorer family-tree layout (§15.1): a pan-and-zoom node canvas of the rows
// (canvas/Canvas.tsx; the desktop minimap shows when zoomed out) over the
// outline mirror, the same collapsible outline Compact uses, so keyboard and
// screen-reader users get every post with all its integrity elements. Both
// views share which post of each row is centered: clicking a node selects it
// in the outline and, in a row that can re-center, centers it (the rows below
// reload as its replies), like swiping a Cards row. Owner: P1-W09.
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Ref, TreeRowVM, TreeVM } from '../../vm/types';
import { EdgeButton, rowKey } from '../cards/tree/parts';
import { Outline, type OutlineControl } from '../compact/TreeLayout';
import type { TreeActions } from '../types';
import { Canvas } from './canvas/Canvas';
import { layoutTree, type CanvasNode } from './canvas/layout';
import './explorer.css';

export function TreeLayout({ tree, actions }: { tree: TreeVM; actions: TreeActions }) {
  const root = useRef<HTMLDivElement>(null);
  const [chosen, setChosen] = useState<ReadonlyMap<string, Ref>>(new Map());
  const [selected, setSelected] = useState<Ref | undefined>(undefined);

  const centered = useCallback(
    (row: TreeRowVM): Ref | undefined => chosen.get(rowKey(tree.snapshot, row)) ?? row.items[0]?.post.ref,
    [chosen, tree.snapshot],
  );
  const choose = useCallback(
    (row: TreeRowVM, ref: Ref) => {
      if (centered(row) === ref) return;
      setChosen((m) => new Map(m).set(rowKey(tree.snapshot, row), ref));
      actions.recenter(row.gen, ref);
    },
    [actions, centered, tree.snapshot],
  );
  const control = useMemo<OutlineControl>(() => ({ centered, choose, ...(selected ? { selected } : {}) }), [centered, choose, selected]);
  const layout = useMemo(() => layoutTree(tree, centered), [tree, centered]);

  const onSelect = (n: CanvasNode) => {
    setSelected(n.ref);
    // Rows two or more above stay put (as in every format).
    const row = tree.rows.find((r) => r.gen === n.gen);
    if (row && row.gen >= -1 && !n.centered) choose(row, n.ref);
  };

  // The outline follows the canvas selection.
  useEffect(() => {
    if (!selected) return;
    const item: Partial<Element> | null | undefined = root.current?.querySelector(
      `.tree-outline [data-testid="tree-item"][data-ref="${CSS.escape(selected)}"]`,
    );
    item?.scrollIntoView?.({ block: 'nearest', behavior: 'smooth' });
  }, [selected]);

  return (
    <div className="tree tree-explorer" data-format="explorer" data-testid="tree-layout" ref={root}>
      {tree.hasEarlier ? <EdgeButton kind="earlier" onClick={actions.loadEarlier} /> : null}
      <Canvas layout={layout} rows={tree.rows} focal={layout.byRef.get(tree.focal)} selected={selected} onSelect={onSelect} />
      <Outline tree={tree} actions={actions} control={control} />
      {tree.hasLater ? <EdgeButton kind="later" onClick={actions.loadLater} /> : null}
    </div>
  );
}
