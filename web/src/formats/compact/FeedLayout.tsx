// Compact feed layout: dense rows under the same API tier markers as Cards
// (never derived from post content), threads as an indented outline whose
// earlier parts unfold on request, collapsed low-trust groups as one row.
// Owner: P0-W07 → P1-W12.
import { useTranslation } from 'react-i18next';
import { useSeenTracker } from '../../features/engage';
import { TierHeader } from '../../integrity';
import type { FeedItemVM, FeedPageVM } from '../../vm/types';
import { asRef } from '../../vm/types';
import type { PostActions } from '../types';
import { CollapsedGroup, LoadMore, ThreadCard } from '../cards/feed/items';
import { groupTiers, itemKey, SeenTrackProvider } from '../cards/feed/parts';
import { PostLayout } from './PostLayout';
import './feed/compact.css';

interface Props {
  page: FeedPageVM[];
  actions: PostActions;
  onLoadMore: () => void;
  onExpand: (token: string) => void;
}

function Row({ item, actions, onExpand }: { item: FeedItemVM; actions: PostActions; onExpand: (token: string) => void }) {
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
  const { t } = useTranslation('feed');
  const markSeen = actions.markSeen;
  const track = useSeenTracker(
    markSeen
      ? (ref: string) => {
          markSeen(asRef(ref));
        }
      : undefined,
  );
  const { sections, caughtUp } = groupTiers(page);
  const next = page.length > 0 ? page[page.length - 1]?.next : undefined;
  return (
    <SeenTrackProvider track={markSeen ? track : undefined}>
      <div className="feed feed-compact" data-format="compact" data-testid="feed-layout">
        {sections.map((s) => (
          <section key={s.key} className="feed-tier" data-testid="feed-tier" data-tier={s.tier} aria-label={t('tier.section')}>
            {s.start ? <TierHeader tier={s.tier} kind="start" /> : null}
            <ol className="feed-list compact-list">
              {s.items.map((it, i) => (
                <li key={itemKey(it, i)} className="compact-item" data-type={it.type}>
                  <Row item={it} actions={actions} onExpand={onExpand} />
                </li>
              ))}
            </ol>
            {s.end ? <TierHeader tier={s.tier} kind="end" /> : null}
          </section>
        ))}
        {caughtUp ? <TierHeader tier={0} kind="caught_up" /> : null}
        {next ? <LoadMore onLoadMore={onLoadMore} /> : null}
      </div>
    </SeenTrackProvider>
  );
}
