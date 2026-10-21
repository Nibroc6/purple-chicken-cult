// Explorer post layout (§15.1 "cards with a small tree thumbnail"): the Cards
// card, every integrity element included, with the tree thumbnail beside its
// feed variant (a stacked thread's earlier context parts have none). In the
// family tree the outline mirror uses Compact's rows. Owner: P1-W09.
import type { PostVM } from '../../vm/types';
import { PostLayout as Card } from '../cards/PostLayout';
import type { PostActions } from '../types';
import { TreeThumb } from './TreeThumb';
import './explorer.css';

export function PostLayout({
  post,
  actions,
  variant,
  part,
}: {
  post: PostVM;
  actions: PostActions;
  variant: 'feed' | 'tree' | 'focal';
  part?: 'context' | 'ranked';
}) {
  const card = <Card post={post} actions={actions} variant={variant} {...(part ? { part } : {})} />;
  if (variant !== 'feed' || part === 'context') return card;
  return (
    <div className="explorer-card" data-format="explorer" data-testid="explorer-card" data-ref={post.ref}>
      {card}
      <TreeThumb post={post} actions={actions} />
    </div>
  );
}
