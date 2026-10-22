// Format contract (SCAFFOLD; plan §6.3). A format is ONLY layout: components
// over view-models and callbacks. Formats never import src/api (ESLint), so they
// cannot differ in what they reveal.
import type { ComponentType } from 'react';
import type { ReportTarget } from '../integrity/types';
import type { FeedPageVM, PostVM, Ref, TreeVM } from '../vm/types';

export interface PostActions {
  like: (post: Ref, on: boolean) => void;
  reply: (post: Ref) => void;
  openTree: (post: Ref) => void;
  hide: (post: Ref) => void;
  showLess: (post: Ref) => void;
  // save sets or clears the viewer's private save (P1-W08; saved_by_me is the state).
  save?: (post: Ref, on: boolean) => void;
  report: (target: ReportTarget) => void;
  markSeen?: (post: Ref) => void;
}

export interface TreeActions extends PostActions {
  more: (gen: number, center: Ref, cursor: string) => void;
  loadEarlier: () => void;
  loadLater: () => void;
}

export type FormatId = 'cards' | 'compact' | 'explorer';

export interface FormatModule {
  id: FormatId;
  FeedLayout: ComponentType<{ page: FeedPageVM[]; actions: PostActions; onLoadMore: () => void; onExpand: (token: string) => void }>;
  PostLayout: ComponentType<{ post: PostVM; actions: PostActions; variant: 'feed' | 'tree' | 'focal' }>;
  TreeLayout: ComponentType<{ tree: TreeVM; actions: TreeActions }>;
}
