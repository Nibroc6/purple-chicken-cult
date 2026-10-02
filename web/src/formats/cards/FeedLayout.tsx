// Cards (default) feed layout (SCAFFOLD hand-off stub). Owner: P0-W07 → P1-W12.
// Tier headers come from API markers only (§10.1.7), never from post content.
import { TierHeader } from '../../integrity';
import type { FeedPageVM } from '../../vm/types';
import type { PostActions } from '../types';
import { PostLayout } from './PostLayout';

export function FeedLayout({ page, actions }: { page: FeedPageVM[]; actions: PostActions; onLoadMore: () => void; onExpand: (token: string) => void }) {
  return (
    <div className="feed" data-format="cards">
      {page.flatMap((p) =>
        p.items.map((item, i) => {
          const key = `${p.session}-${String(i)}`;
          switch (item.type) {
            case 'tier_start':
              return <TierHeader key={key} tier={item.tier} kind="start" />;
            case 'tier_end':
              return <TierHeader key={key} tier={item.tier} kind="end" />;
            case 'caught_up':
              return <TierHeader key={key} tier={0} kind="caught_up" />;
            case 'post':
              return <PostLayout key={key} post={item.post} actions={actions} variant="feed" />;
            case 'thread':
              return item.parts.map((part) => <PostLayout key={part.ref} post={part} actions={actions} variant="feed" />);
            case 'collapsed':
              return null;
          }
          return null;
        }),
      )}
    </div>
  );
}
