// Cards feed layout (§10.1.7): single-column cards under small sticky tier
// headers that come only from the API's markers (tier_start / tier_end /
// caught_up), a stacked card per continuation thread (earlier parts folded
// away as context), one row per collapsed group of distant low-trust posts,
// and "load more" while the API offers a next page. One seen tracker serves
// the whole feed; only a card's own post is observed.
// Owner: P0-W07 → P1-W12.
import { useTranslation } from 'react-i18next';
import { useSeenTracker } from '../../features/engage';
import { TierHeader } from '../../integrity';
import type { FeedItemVM, FeedPageVM } from '../../vm/types';
import { asRef } from '../../vm/types';
import type { PostActions } from '../types';
import { groupTiers, itemKey, SeenTrackProvider } from './feed/parts';
import { CollapsedGroup, LoadMore, ThreadCard } from './feed/items';
import { PostLayout } from './PostLayout';
import './feed/cards.css';

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
      <div className="feed feed-cards" data-format="cards" data-testid="feed-layout">
        {sections.map((s) => (
          <section key={s.key} className="feed-tier" data-testid="feed-tier" data-tier={s.tier} aria-label={t('tier.section')}>
            {s.start ? <TierHeader tier={s.tier} kind="start" /> : null}
            <ol className="feed-list">
              {s.items.map((it, i) => (
                <li key={itemKey(it, i)} className="feed-item" data-type={it.type}>
                  <Item item={it} actions={actions} onExpand={onExpand} />
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
