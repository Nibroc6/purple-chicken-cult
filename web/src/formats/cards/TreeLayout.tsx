// Cards family-tree layout (§10.1.4): one horizontal row per generation under
// a small section header, the focal post centered in its row. The centered
// post of each row is its best-ranked one, so the vertical path runs through
// the best post of every generation; lower ranks sit to the side, smaller and
// faded — to the right on mobile, alternating sides on desktop — and after
// five a "+N more" chip opens the rest as a ranked list. Swiping a row
// (horizontal scroll-snap) or its previous / next buttons (keyboard) re-centers
// it, and the rows below reload as the new center's replies. A hairline marks
// each change of distance tier inside a row. Posts longer than 10 lines fold
// (by keyboard and screen reader too); the focal post never folds.
// Owner: P0-W08 → P1-W09.
import { useEffect, useId, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { Icon } from '../../lib/Icon';
import type { Ref, TreeRowVM, TreeVM } from '../../vm/types';
import type { TreeActions } from '../types';
import { PostLayout } from './PostLayout';
import {
  EdgeButton,
  MoreChip,
  MoreList,
  RowHeader,
  TierDivider,
  TreeItemView,
  VISIBLE,
  rowKey,
  useRowCenter,
  useToggle,
} from './tree/parts';

// side is a slot's place on desktop: the best post in the middle, then
// alternating right and left (r1, l1, r2, l2).
export function side(rank: number): string {
  if (rank === 0) return 'center';
  return `${rank % 2 === 1 ? 'r' : 'l'}${String(Math.ceil(rank / 2))}`;
}

function scrollToSlot(el: Partial<Element> | null | undefined) {
  el?.scrollIntoView?.({ block: 'nearest', inline: 'center', behavior: 'smooth' });
}

function Row({ row, focal, actions }: { row: TreeRowVM; focal: Ref; actions: TreeActions }) {
  const { t } = useTranslation('tree');
  const headerId = useId();
  const [centered, choose] = useRowCenter(row, actions);
  const [listOpen, toggleList, listId] = useToggle();
  const track = useRef<HTMLOListElement>(null);
  const centeredRef = useRef(centered);
  useEffect(() => {
    centeredRef.current = centered;
  }, [centered]);
  const shown = row.items.slice(0, VISIBLE);
  const idx = Math.max(
    0,
    shown.findIndex((i) => i.post.ref === centered),
  );

  // A swipe ends with a slot snapped to the middle: that post is the new center.
  useEffect(() => {
    const el = track.current;
    if (!el) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const settle = () => {
      const box = el.getBoundingClientRect();
      if (box.width === 0) return;
      const mid = box.left + box.width / 2;
      let best: { ref: string; d: number } | undefined;
      el.querySelectorAll<HTMLElement>('[data-slot-ref]').forEach((s) => {
        const b = s.getBoundingClientRect();
        const d = Math.abs(b.left + b.width / 2 - mid);
        if (!best || d < best.d) best = { ref: s.dataset.slotRef ?? '', d };
      });
      if (best && best.ref !== centeredRef.current) choose(best.ref as Ref);
    };
    const onScroll = () => {
      clearTimeout(timer);
      timer = setTimeout(settle, 150);
    };
    el.addEventListener('scroll', onScroll, { passive: true });
    return () => {
      clearTimeout(timer);
      el.removeEventListener('scroll', onScroll);
    };
  });

  // The centered post starts in the middle of the row.
  useEffect(() => {
    scrollToSlot(track.current?.querySelector('[data-centered="true"]'));
  }, []);

  const go = (i: number) => {
    const it = shown[i];
    if (!it) return;
    choose(it.post.ref);
    scrollToSlot(track.current?.querySelector(`[data-slot-ref="${CSS.escape(it.post.ref)}"]`));
  };

  return (
    <section
      className="tree-row"
      data-testid="tree-row"
      data-gen={row.gen}
      data-label={row.label}
      data-focal-row={row.gen === 0}
      aria-labelledby={headerId}
    >
      <RowHeader row={row} id={headerId} />
      <div className="tree-row-body">
        <ol className="tree-track" ref={track} data-testid="tree-track">
          {shown.map((it, i) => (
            <li
              key={it.post.ref}
              className="tree-slot"
              data-testid="tree-item"
              data-ref={it.post.ref}
              data-slot-ref={it.post.ref}
              data-side={side(i)}
              data-centered={it.post.ref === centered}
              data-divider={it.tierDivider}
              aria-current={it.post.ref === centered ? 'true' : undefined}
            >
              {it.tierDivider ? <TierDivider tier={it.post.tier} /> : null}
              <TreeItemView item={it} focal={focal} actions={actions} Post={PostLayout} />
            </li>
          ))}
        </ol>
        {shown.length > 1 ? (
          <div className="tree-row-nav" role="group" aria-label={t('row.nav')}>
            <button
              type="button"
              data-testid="tree-prev"
              aria-label={t('row.prev')}
              disabled={idx === 0}
              onClick={() => {
                go(idx - 1);
              }}
            >
              <Icon name="prev" />
            </button>
            <span className="muted tree-row-pos">{t('row.position', { index: idx + 1, total: shown.length })}</span>
            <button
              type="button"
              data-testid="tree-next"
              aria-label={t('row.next')}
              disabled={idx >= shown.length - 1}
              onClick={() => {
                go(idx + 1);
              }}
            >
              <Icon name="next" />
            </button>
          </div>
        ) : null}
        <MoreChip row={row} open={listOpen} controls={listId} onToggle={toggleList} />
      </div>
      {listOpen ? <MoreList row={row} id={listId} focal={focal} actions={actions} Post={PostLayout} /> : null}
    </section>
  );
}

export function TreeLayout({ tree, actions }: { tree: TreeVM; actions: TreeActions }) {
  const { t } = useTranslation('tree');
  return (
    <div className="tree tree-cards" data-format="cards" data-testid="tree-layout" aria-label={t('layout.label')}>
      {tree.hasEarlier ? <EdgeButton kind="earlier" onClick={actions.loadEarlier} /> : null}
      {tree.rows.map((row) => (
        <Row key={rowKey(tree.snapshot, row)} row={row} focal={tree.focal} actions={actions} />
      ))}
      {tree.hasLater ? <EdgeButton kind="later" onClick={actions.loadLater} /> : null}
    </div>
  );
}
