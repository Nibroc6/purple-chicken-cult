// The reply tree (§10.1.4), the same in every format: the whole tree on one
// pannable, zoomable canvas (canvas/Canvas.tsx), every post a card joined to
// its parents and replies by family-tree lines, the focal post highlighted in
// the middle. Clicking a post brings it into focus (the tree reopens around
// it). The focused post is shown in full on a card floating over the map
// (a bottom sheet on phones) that folds down to its bar, in the format's own
// post layout, with all its integrity elements and actions; keyboard and
// screen-reader users reach every other post as a button on the map.
// Every format's TreeLayout renders it with its own post layout.
// Owner: P0-W08 → P1-W09.
import { useId, useMemo, useState, type ComponentType } from 'react';
import { useTranslation } from 'react-i18next';
import type { PostVM, TreeVM } from '../../vm/types';
import type { FormatId, PostActions, TreeActions } from '../types';
import { Canvas } from './canvas/Canvas';
import { layoutTree } from './canvas/layout';

// WIDE matches screens where the focus card floats beside the map rather than
// being a bottom sheet (canvas.css).
const WIDE = '(min-width: 48rem)';

export type PostComponent = ComponentType<{
  post: PostVM;
  actions: PostActions;
  variant: 'feed' | 'tree' | 'focal';
}>;

function EdgeButton({ kind, onClick }: { kind: 'earlier' | 'later'; onClick: () => void }) {
  const { t } = useTranslation('tree');
  return (
    <button type="button" className="tree-edge" data-testid={`tree-${kind}`} onClick={onClick}>
      {kind === 'earlier' ? t('edge.earlier') : t('edge.later')}
    </button>
  );
}

export function ConnectedTree({
  tree,
  actions,
  format,
  Post,
}: {
  tree: TreeVM;
  actions: TreeActions;
  format: FormatId;
  Post: PostComponent;
}) {
  const { t } = useTranslation('tree');
  const uid = useId().replace(/:/g, '');
  // The focused post's card folds down to its bar to free the map.
  // On phones the focused post starts folded so the map shows first.
  const [open, setOpen] = useState(() => typeof window.matchMedia !== 'function' || window.matchMedia(WIDE).matches);
  // Each row hangs from its first post (the server's best-ranked one).
  const layout = useMemo(() => layoutTree(tree, () => undefined), [tree]);
  const focal = tree.rows.flatMap((r) => r.items).find((i) => i.post.ref === tree.focal && !i.shownAbove)?.post;
  const tools = (
    <>
      {tree.hasEarlier ? <EdgeButton kind="earlier" onClick={actions.loadEarlier} /> : null}
      {tree.hasLater ? <EdgeButton kind="later" onClick={actions.loadLater} /> : null}
    </>
  );
  return (
    <div className="tree tree-connected" data-format={format} data-testid="tree-layout" aria-label={t('layout.label')} role="region">
      <Canvas
        layout={layout}
        focal={layout.byRef.get(tree.focal)}
        tools={tools}
        onFocus={(n) => {
          actions.openTree(n.ref);
        }}
        onMore={(m) => {
          if (m.cursor) actions.more(m.gen, m.center, m.cursor);
        }}
      />
      {focal ? (
        <section
          className="tree-focus tree-overlay"
          data-testid="tree-focus"
          data-ref={focal.ref}
          data-open={open}
          aria-labelledby={`${uid}-title`}
        >
          <div className="tree-focus-bar">
            <h2 id={`${uid}-title`} className="tree-focus-title">
              {t('focus.title')}
            </h2>
            <button
              type="button"
              className="tree-focus-toggle"
              data-testid="tree-focus-toggle"
              aria-expanded={open}
              aria-controls={`${uid}-body`}
              onClick={() => {
                setOpen((o) => !o);
              }}
            >
              {open ? t('focus.hide') : t('focus.show')}
            </button>
          </div>
          <div id={`${uid}-body`} className="tree-focus-body">
            <Post post={focal} actions={actions} variant="focal" />
          </div>
        </section>
      ) : null}
    </div>
  );
}
