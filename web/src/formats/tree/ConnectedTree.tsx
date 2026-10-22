// The reply tree (§10.1.4), the same in every format: the whole tree on one
// pannable, zoomable canvas (canvas/Canvas.tsx), every post a card joined to
// its parents and replies by family-tree lines, the focal post highlighted in
// the middle. Clicking a post brings it into focus (the tree reopens around
// it). The focused post is shown in full under the map, in the format's own
// post layout, with all its integrity elements and actions; keyboard and
// screen-reader users reach every other post as a button on the map.
// Every format's TreeLayout renders it with its own post layout.
// Owner: P0-W08 → P1-W09.
import { useMemo, type ComponentType } from 'react';
import { useTranslation } from 'react-i18next';
import type { PostVM, TreeVM } from '../../vm/types';
import type { FormatId, PostActions, TreeActions } from '../types';
import { Canvas } from './canvas/Canvas';
import { layoutTree } from './canvas/layout';

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
        <section className="tree-focus" data-testid="tree-focus" data-ref={focal.ref} aria-labelledby="tree-focus-title">
          <h2 id="tree-focus-title" className="visually-hidden">
            {t('focus.title')}
          </h2>
          <Post post={focal} actions={actions} variant="focal" />
        </section>
      ) : null}
    </div>
  );
}
