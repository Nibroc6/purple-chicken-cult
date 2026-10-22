// Explorer feed layout (§15.1): the Cards feed — one list with no per-tier
// headers (§10.1.7: only the caught_up marker), stacked thread cards, collapsed low-trust
// groups, "load more" — with Explorer cards, each carrying its tree thumbnail.
// One seen tracker serves the whole feed. Owner: P1-W09.
import { useSeenTracker } from '../../features/engage';
import { TierHeader } from '../../integrity';
import type { FeedItemVM, FeedPageVM } from '../../vm/types';
import { asRef } from '../../vm/types';
import { feedItems, itemKey, SeenTrackProvider } from '../cards/feed/parts';
import { CollapsedGroup, LoadMore, ThreadCard } from '../cards/feed/items';
import type { PostActions } from '../types';
import { PostLayout } from './PostLayout';
import '../cards/feed/cards.css';

interface Props {
  page: FeedPageVM[];
  actions: PostActions;
  onLoadMore: () => void;
  onExpand: (token: string) => void;
}

function Item({ item, actions, onExpand }: { item: FeedItemVM; actions: PostActions; onExpand: (token: string) => void }) {
  switch (item.type) {
    case 'post':
      return <PostLayout post={item.post} actions={actions} variant="feed" />;
    case 'thread':
      return <ThreadCard parts={item.parts} actions={actions} Post={PostLayout} />;
    case 'collapsed':
      return <CollapsedGroup count={item.count} token={item.token} onExpand={onExpand} />;
    default:
      return null;
  }
}

export function FeedLayout({ page, actions, onLoadMore, onExpand }: Props) {
  const markSeen = actions.markSeen;
  const track = useSeenTracker(
    markSeen
      ? (ref: string) => {
          markSeen(asRef(ref));
        }
      : undefined,
  );
  const { items, caughtUp } = feedItems(page);
  const next = page.length > 0 ? page[page.length - 1]?.next : undefined;
  return (
    <SeenTrackProvider track={markSeen ? track : undefined}>
      <div className="feed feed-cards feed-explorer" data-format="explorer" data-testid="feed-layout">
        <ol className="feed-list">
          {items.map((it, i) => (
            <li key={itemKey(it, i)} className="feed-item" data-type={it.type}>
              <Item item={it} actions={actions} onExpand={onExpand} />
            </li>
          ))}
        </ol>
        {caughtUp ? <TierHeader tier={0} kind="caught_up" /> : null}
        {next ? <LoadMore onLoadMore={onLoadMore} /> : null}
      </div>
    </SeenTrackProvider>
  );
}
