// Compact family-tree layout (§10.1.4): a collapsible, indented outline — one
// disclosure per generation (its section header is the button), the posts of
// the row as dense rows in ranked order with the centered one marked, "+N
// more" after five, and "show its replies" to re-center a row (the rows below
// reload). Tier changes inside a row are hairlines; posts longer than 10 lines
// fold. The same outline serves keyboard and screen-reader users of every
// format: Explorer mirrors its canvas with it (Outline), steering which post
// of each row is centered and which one the canvas selected. Owner: P0-W08 →
// P1-W09.
import { useTranslation } from 'react-i18next';
import type { Ref, TreeRowVM, TreeVM } from '../../vm/types';
import type { TreeActions } from '../types';
import {
  CenterButton,
  EdgeButton,
  MoreChip,
  MoreList,
  TierDivider,
  TreeItemView,
  VISIBLE,
  rowKey,
  useRowCenter,
  useToggle,
} from '../cards/tree/parts';
import { PostLayout } from './PostLayout';
import './tree/outline.css';

// OutlineControl lets another view (Explorer's canvas) own the centered post
// of each row and mark a selected post.
export interface OutlineControl {
  centered: (row: TreeRowVM) => Ref | undefined;
  choose: (row: TreeRowVM, ref: Ref) => void;
  selected?: Ref;
}

// i18n-dynamic: tree:row.
function OutlineRow({
  row,
  depth,
  focal,
  actions,
  control,
}: {
  row: TreeRowVM;
  depth: number;
  focal: Ref;
  actions: TreeActions;
  control?: OutlineControl;
}) {
  const { t } = useTranslation('tree');
  const [open, toggle, id] = useToggle(true);
  const [listOpen, toggleList, listId] = useToggle();
  const [ownCentered, ownChoose] = useRowCenter(row, actions);
  const centered = control ? control.centered(row) : ownCentered;
  const choose = control
    ? (ref: Ref) => {
        control.choose(row, ref);
      }
    : ownChoose;
  // Re-centering lists other replies below; rows two or more above stay put.
  const canCenter = row.gen >= -1;
  return (
    <li className="tree-outline-row" data-testid="tree-row" data-gen={row.gen} data-label={row.label} data-depth={Math.min(depth, 6)}>
      <button
        type="button"
        className="tree-outline-toggle"
        data-testid="tree-row-toggle"
        aria-expanded={open}
        aria-controls={id}
        onClick={toggle}
      >
        <span data-testid="tree-row-header">{t(`row.${row.label}` as 'row.replies', { count: Math.abs(row.gen) })}</span>{' '}
        <span className="muted">{t('outline.count', { count: row.items.length + row.more })}</span>
      </button>
      <div id={id} hidden={!open}>
        <ol className="tree-outline-items">
          {row.items.slice(0, VISIBLE).map((it) => (
            <li
              key={it.post.ref}
              className="tree-outline-item"
              data-testid="tree-item"
              data-ref={it.post.ref}
              data-centered={it.post.ref === centered}
              data-selected={control?.selected === it.post.ref ? 'true' : undefined}
              aria-current={it.post.ref === centered ? 'true' : undefined}
            >
              {it.tierDivider ? <TierDivider tier={it.post.tier} /> : null}
              <TreeItemView item={it} focal={focal} actions={actions} Post={PostLayout} />
              {canCenter && !it.shownAbove && it.post.ref !== centered ? (
                <CenterButton
                  onCenter={() => {
                    choose(it.post.ref);
                  }}
                />
              ) : null}
            </li>
          ))}
        </ol>
        <MoreChip row={row} open={listOpen} controls={listId} onToggle={toggleList} />
        {listOpen ? (
          <MoreList
            row={row}
            id={listId}
            focal={focal}
            actions={actions}
            Post={PostLayout}
            {...(canCenter ? { onCenter: choose } : {})}
          />
        ) : null}
      </div>
    </li>
  );
}

// Outline is the indented outline of every row of the tree.
export function Outline({ tree, actions, control }: { tree: TreeVM; actions: TreeActions; control?: OutlineControl }) {
  const { t } = useTranslation('tree');
  return (
    <ol className="tree-outline" aria-label={t('layout.label')}>
      {tree.rows.map((row, i) => (
        <OutlineRow
          key={rowKey(tree.snapshot, row)}
          row={row}
          depth={i}
          focal={tree.focal}
          actions={actions}
          {...(control ? { control } : {})}
        />
      ))}
    </ol>
  );
}

export function TreeLayout({ tree, actions }: { tree: TreeVM; actions: TreeActions }) {
  return (
    <div className="tree tree-compact" data-format="compact" data-testid="tree-layout">
      {tree.hasEarlier ? <EdgeButton kind="earlier" onClick={actions.loadEarlier} /> : null}
      <Outline tree={tree} actions={actions} />
      {tree.hasLater ? <EdgeButton kind="later" onClick={actions.loadLater} /> : null}
    </div>
  );
}
