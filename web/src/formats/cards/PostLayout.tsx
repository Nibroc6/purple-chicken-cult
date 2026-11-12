// Cards post layout (§10.1.7): an author row (display name with its @handle,
// the time, "why"), a provenance row (chain, inviter label), the visible
// context rows, the body (folded past 6 lines or 400 characters in the feed,
// 10 lines in the tree), link pictures (P1), previews, then one row: the like
// summary over like / save, reply, family tree and a ⋯ menu (add to a reply,
// hide, show less), with the audience, edited mark and report at its end.
// Every integrity element (§15.5) stays visible on the card.
// Owner: P0-W07 → P1-W12 (P0-W08 reuses it for the tree's 'tree' / 'focal' variants).
import { useTranslation } from 'react-i18next';
import { ReplyPick } from '../../features/composer';
import { EngageBar, EngageMenuItems, useHidden } from '../../features/engage';
import { MoreMenu } from '../../lib/MoreMenu';
import { AudienceBadge, Chain, EditedMark, InviterLabel, ReportAction, WhyButton } from '../../integrity';
import type { PostVM } from '../../vm/types';
import type { PostActions } from '../types';
import {
  ContextRows,
  FoldableBody,
  MediaList,
  mediaVariant,
  PersonLink,
  PostLinks,
  PostTime,
  PreviewList,
  shouldFold,
  useSeenTarget,
} from './feed/parts';
import './feed/cards.css';

export interface CardProps {
  post: PostVM;
  actions: PostActions;
  variant: 'feed' | 'tree' | 'focal';
  // part: a part of a stacked thread card. 'context' parts are earlier parts
  // shown for context (never seen-tracked); 'ranked' is the card's own post.
  part?: 'context' | 'ranked';
}

export function PostLayout({ post, actions, variant, part }: CardProps) {
  const { t } = useTranslation('feed');
  const hidden = useHidden(post.ref);
  const own = variant === 'feed' && part !== 'context' && !hidden;
  const seen = useSeenTarget(post.ref, own);
  return (
    <article
      className={`post post-card post-${variant}`}
      data-format="cards"
      data-testid="feed-post"
      data-ref={post.ref}
      data-tier={post.tier}
      data-part={part}
      {...seen}
    >
      <header className="post-author">
        <PersonLink person={post.author.ref} className="post-who">
          {post.author.displayName ? <span className="post-name" data-user-content="">{post.author.displayName}</span> : null}{' '}
          <span className="post-handle">{t('post.handle', { handle: post.author.handle })}</span>
        </PersonLink>
        <span className="post-head-end">
          <PostTime at={post.createdAt} />
          {post.thread ? (
            <span className="post-thread muted" data-testid="feed-thread-pos">
              {t('thread.position', { index: post.thread.index, total: post.thread.total })}
            </span>
          ) : null}
          <WhyButton post={post.ref} />
        </span>
      </header>
      <div className="post-provenance">
        <Chain chain={post.author.chain} author={post.author} />
        <InviterLabel inviter={post.author.inviter} invitee={post.author.ref} onReport={actions.report} />
      </div>
      {hidden ? null : (
        <>
          <ContextRows rows={post.context} withCont={part === undefined} />
          {post.body ? <FoldableBody doc={post.body} fold={shouldFold(post, variant)} /> : null}
          <MediaList media={post.media} variant={mediaVariant(variant)} />
          <PreviewList previews={post.previews} />
        </>
      )}
      <footer className="post-actions">
        {part === 'context' ? null : (
          <>
            <EngageBar post={post} actions={actions} withMenu={false} />
            {hidden ? null : <PostLinks post={post} actions={actions} pick={false} />}
            {hidden || (post.mine && !post.canReference) ? null : (
              <MoreMenu label={t('post.more')} testId="post-more">
                {post.canReference ? <ReplyPick post={post} /> : null}
                <EngageMenuItems post={post} actions={actions} />
              </MoreMenu>
            )}
          </>
        )}
        <span className="post-meta">
          <AudienceBadge audience={post.audience} forced={post.audienceForced} />
          <EditedMark {...(post.editedAt ? { editedAt: post.editedAt } : {})} />
          <ReportAction target={{ kind: 'post', ref: post.ref }} onReport={actions.report} />
        </span>
      </footer>
    </article>
  );
}
