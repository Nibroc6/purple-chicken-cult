// Cards (default) tree layout (SCAFFOLD hand-off stub). Owner: P0-W08 → P1-W09.
import type { TreeVM } from '../../vm/types';
import type { TreeActions } from '../types';
import { PostLayout } from './PostLayout';

export function TreeLayout({ tree, actions }: { tree: TreeVM; actions: TreeActions }) {
  return (
    <div className="tree" data-format="cards">
      {tree.rows.map((row) => (
        <section key={`${String(row.gen)}-${row.center}`} className="tree-row" data-gen={row.gen}>
          {row.items.map((it) => (
            <PostLayout key={it.post.ref} post={it.post} actions={actions} variant={row.gen === 0 ? 'focal' : 'tree'} />
          ))}
        </section>
      ))}
    </div>
  );
}
