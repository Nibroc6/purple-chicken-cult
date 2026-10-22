// Cards reply tree (§10.1.4): the connected canvas every format shares
// (formats/tree/ConnectedTree.tsx), the focused post in Cards's own post
// layout. Owner: P0-W08 → P1-W09.
import type { TreeVM } from '../../vm/types';
import { ConnectedTree } from '../tree/ConnectedTree';
import type { TreeActions } from '../types';
import { PostLayout } from './PostLayout';

export function TreeLayout({ tree, actions }: { tree: TreeVM; actions: TreeActions }) {
  return <ConnectedTree tree={tree} actions={actions} format="cards" Post={PostLayout} />;
}
