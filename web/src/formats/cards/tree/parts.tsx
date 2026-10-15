// Building blocks of the Cards and Compact family-tree layouts (§10.1.4; layout
// only: view-models and callbacks in, no API). Owner: P0-W08 → P1-W09.
//
// - RowHeader: the small section header of every row ("Replying to", "This
//   post", "Replies", "3 generations down", …), from the row label only.
// - TierDivider: the hairline where the distance tier changes inside a row.
// - TreeItemView: a post of a row (the format's own post layout, variant
//   'focal' for the focal post and 'tree' otherwise, so long posts fold at 10
//   lines), "↑ shown above" for a post already shown in a row above, and the
//   "also replies to @b" chips that refocus the tree on another parent.
// - useRowCenter: which post of a row is centered; choosing another re-centers
//   (the rows below reload as its replies).
// - MoreChip / MoreList: "+N more" after five posts opens the rest of the row
//   as a ranked list (N counts visible posts only), with "load more" while the
//   server has further pages.
// - EdgeButton: "load earlier" / "load later" rows.
import { useId, useState, type ComponentType } from 'react';
import { useTranslation } from 'react-i18next';
import type { PostVM, Ref, TreeItemVM, TreeRowVM } from '../../../vm/types';
import type { PostActions, TreeActions } from '../../types';
import './tree.css';

// VISIBLE is the number of posts a row shows before "+N more".
export const VISIBLE = 5;

export type PostComponent = ComponentType<{ post: PostVM; actions: PostActions; variant: 'feed' | 'tree' | 'focal' }>;

// i18n-dynamic: tree:row.
export function RowHeader({ row, id }: { row: TreeRowVM; id: string }) {
  const { t } = useTranslation('tree');
  return (
    <h2 id={id} className="tree-row-header" data-testid="tree-row-header">
      {t(`row.${row.label}` as 'row.replies', { count: Math.abs(row.gen) })}
    </h2>
  );
}

export function TierDivider({ tier }: { tier: number }) {
  const { t } = useTranslation('tree');
  return <span className="tree-divider" role="separator" data-testid="tree-tier-divider" aria-label={t('divider', { tier })} />;
}

function AlsoChips({ item, actions }: { item: TreeItemVM; actions: PostActions }) {
  const { t } = useTranslation('tree');
  if (item.alsoRepliesTo.length === 0) return null;
  return (
    <ul className="tree-also" aria-label={t('item.alsoLabel')}>
      {item.alsoRepliesTo.map((h, i) => {
        const parent = item.alsoParents[i];
        return parent ? (
          <li key={parent}>
            <button
              type="button"
              className="tree-chip"
              data-testid="tree-also"
              onClick={() => {
                actions.openTree(parent);
              }}
            >
              {t('item.also', { handle: h.handle })}
            </button>
          </li>
        ) : null;
      })}
    </ul>
  );
}

export function ShownAbove({ item }: { item: TreeItemVM }) {
  const { t } = useTranslation('tree');
  return (
    <p className="tree-shown-above muted" data-testid="tree-shown-above" data-ref={item.post.ref}>
      {t('item.shownAbove', { handle: item.post.author.handle })}
    </p>
  );
}

export function TreeItemView({
  item,
  focal,
  actions,
  Post,
}: {
  item: TreeItemVM;
  focal: Ref;
  actions: PostActions;
  Post: PostComponent;
}) {
  if (item.shownAbove) return <ShownAbove item={item} />;
  return (
    <>
      <Post post={item.post} actions={actions} variant={item.post.ref === focal ? 'focal' : 'tree'} />
      <AlsoChips item={item} actions={actions} />
    </>
  );
}

// useRowCenter keeps the centered post of a row (the row's first post until the
// reader picks another). choose re-centers: the rows below reload.
export function useRowCenter(row: TreeRowVM, actions: TreeActions): [Ref | undefined, (ref: Ref) => void] {
  const [centered, setCentered] = useState<Ref | undefined>(row.items[0]?.post.ref);
  const choose = (ref: Ref) => {
    if (ref === centered) return;
    setCentered(ref);
    actions.recenter(row.gen, ref);
  };
  return [centered, choose];
}

// extraCount is N of "+N more": visible posts of the row beyond the first five.
export function extraCount(row: TreeRowVM): number {
  return Math.max(0, row.items.length - VISIBLE) + row.more;
}

export function MoreChip({ row, open, controls, onToggle }: { row: TreeRowVM; open: boolean; controls: string; onToggle: () => void }) {
  const { t } = useTranslation('tree');
  const n = extraCount(row);
  if (n === 0) return null;
  return (
    <button
      type="button"
      className="tree-chip tree-more"
      data-testid="tree-more"
      data-count={n}
      aria-expanded={open}
      aria-controls={controls}
      aria-label={t('row.moreLabel', { count: n })}
      onClick={onToggle}
    >
      {t('row.more', { count: n })}
    </button>
  );
}

export function MoreList({
  row,
  id,
  focal,
  actions,
  Post,
  onCenter,
}: {
  row: TreeRowVM;
  id: string;
  focal: Ref;
  actions: TreeActions;
  Post: PostComponent;
  onCenter?: (ref: Ref) => void;
}) {
  const { t } = useTranslation('tree');
  const rest = row.items.slice(VISIBLE);
  return (
    <div id={id} className="tree-more-list" data-testid="tree-more-list">
      <ol className="tree-list">
        {rest.map((it) => (
          <li key={it.post.ref} className="tree-list-item" data-testid="tree-item" data-ref={it.post.ref}>
            {it.tierDivider ? <TierDivider tier={it.post.tier} /> : null}
            <TreeItemView item={it} focal={focal} actions={actions} Post={Post} />
            {onCenter && !it.shownAbove ? <CenterButton
                onCenter={() => {
                  onCenter(it.post.ref);
                }}
              /> : null}
          </li>
        ))}
      </ol>
      {row.cursor ? (
        <button
          type="button"
          className="tree-load-more"
          data-testid="tree-load-more"
          onClick={() => {
            if (row.cursor) actions.more(row.gen, row.center, row.cursor);
          }}
        >
          {t('row.loadMore')}
        </button>
      ) : null}
    </div>
  );
}

// CenterButton centers a post of a row (its replies load below).
export function CenterButton({ onCenter }: { onCenter: () => void }) {
  const { t } = useTranslation('tree');
  return (
    <button type="button" className="tree-center" data-testid="tree-center" onClick={onCenter}>
      {t('item.center')}
    </button>
  );
}

export function EdgeButton({ kind, onClick }: { kind: 'earlier' | 'later'; onClick: () => void }) {
  const { t } = useTranslation('tree');
  return (
    <button type="button" className="tree-edge" data-testid={`tree-${kind}`} onClick={onClick}>
      {kind === 'earlier' ? t('edge.earlier') : t('edge.later')}
    </button>
  );
}

// useToggle is a disclosure state with the id its button controls.
export function useToggle(initial = false): [boolean, () => void, string] {
  const [open, setOpen] = useState(initial);
  const id = useId();
  return [
    open,
    () => {
      setOpen((o) => !o);
    },
    id,
  ];
}

// rowKey is a stable key for a row of one snapshot.
export function rowKey(snapshot: string, row: TreeRowVM): string {
  return `${snapshot}:${String(row.gen)}:${row.center}`;
}
