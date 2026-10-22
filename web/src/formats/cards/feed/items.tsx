// Feed items shared by the Cards and Compact layouts: the stacked thread card,
// the collapsed low-trust group and "load more". Owner: P0-W07 → P1-W12.
import { useId, useState, type ComponentType } from 'react';
import { useTranslation } from 'react-i18next';
import type { PostVM } from '../../../vm/types';
import type { PostActions } from '../../types';
import { InfoTip } from '../../../lib/InfoTip';

type PartLayout = ComponentType<{
  post: PostVM;
  actions: PostActions;
  variant: 'feed' | 'tree' | 'focal';
  part?: 'context' | 'ranked';
}>;

// ThreadCard is a continuation thread as one feed item (§10.1.2): ranked and
// seen-tracked as its last part, the earlier visible parts folded away above
// it as context ("1/3, 2/3…").
export function ThreadCard({ parts, actions, Post }: { parts: PostVM[]; actions: PostActions; Post: PartLayout }) {
  const { t } = useTranslation('feed');
  const id = useId();
  const [open, setOpen] = useState(false);
  const earlier = parts.slice(0, -1);
  const ranked = parts[parts.length - 1];
  if (!ranked) return null;
  return (
    <div className="feed-thread" data-testid="feed-thread" data-parts={parts.length}>
      {earlier.length > 0 ? (
        <>
          <button
            type="button"
            className="feed-thread-toggle"
            data-testid="feed-thread-toggle"
            aria-expanded={open}
            aria-controls={id}
            onClick={() => {
              setOpen((o) => !o);
            }}
          >
            {open ? t('thread.hideEarlier') : t('thread.showEarlier', { count: earlier.length })}
          </button>
          <ol id={id} className="feed-thread-earlier" hidden={!open}>
            {earlier.map((p) => (
              <li key={p.ref}>
                <Post post={p} actions={actions} variant="feed" part="context" />
              </li>
            ))}
          </ol>
        </>
      ) : null}
      <Post post={ranked} actions={actions} variant="feed" part="ranked" />
    </div>
  );
}

// CollapsedGroup: "3 posts from distant, low-trust connections" (§4.4), opened
// on request only.
export function CollapsedGroup({ count, token, onExpand }: { count: number; token: string; onExpand: (token: string) => void }) {
  const { t } = useTranslation('feed');
  return (
    <div className="feed-collapsed" data-testid="feed-collapsed" data-count={count}>
      <p>
        {t('collapsed.summary', { count })}
        <InfoTip label={t('collapsed.explainLabel')}>{t('collapsed.explain')}</InfoTip>
      </p>
      <button
        type="button"
        data-testid="feed-expand"
        onClick={() => {
          onExpand(token);
        }}
      >
        {t('collapsed.show', { count })}
      </button>
    </div>
  );
}

export function LoadMore({ onLoadMore }: { onLoadMore: () => void }) {
  const { t } = useTranslation('feed');
  return (
    <button type="button" className="feed-more" data-testid="feed-more" onClick={onLoadMore}>
      {t('more')}
    </button>
  );
}
