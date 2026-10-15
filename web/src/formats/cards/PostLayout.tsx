// Cards post layout (§10.1.7): an author row (display name with its @handle,
// inviter label, chain, "why"), the visible context rows, the body (folded
// past 6 lines or 400 characters in the feed, 10 lines in the tree), link
// previews, the integrity footer (audience, edited, report) and the actions
// (like summary, like / hide / show less, reply, family tree).
// Owner: P0-W07 → P1-W12 (P0-W08 reuses it for the tree's 'tree' / 'focal' variants).
import { useTranslation } from 'react-i18next';
import { EngageBar, useHidden } from '../../features/engage';
import { AudienceBadge, Chain, EditedMark, InviterLabel, ReportAction, WhyButton } from '../../integrity';
import type { PostVM } from '../../vm/types';
import type { PostActions } from '../types';
import { ContextRows, FoldableBody, PersonLink, PostLinks, PostTime, PreviewList, shouldFold, useSeenTarget } from './feed/parts';
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
          {post.author.displayName ? <span className="post-name">{post.author.displayName}</span> : null}{' '}
          <span className="post-handle">{t('post.handle', { handle: post.author.handle })}</span>
        </PersonLink>
        <InviterLabel inviter={post.author.inviter} invitee={post.author.ref} onReport={actions.report} />
        <Chain chain={post.author.chain} author={post.author} />
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
      {hidden ? null : (
        <>
          <ContextRows rows={post.context} withCont={part === undefined} />
          {post.body ? <FoldableBody doc={post.body} fold={shouldFold(post, variant)} /> : null}
          <PreviewList previews={post.previews} />
        </>
      )}
      <footer className="post-meta">
        <AudienceBadge audience={post.audience} forced={post.audienceForced} />
        <EditedMark {...(post.editedAt ? { editedAt: post.editedAt } : {})} />
        <ReportAction target={{ kind: 'post', ref: post.ref }} onReport={actions.report} />
      </footer>
      {part === 'context' ? null : (
        <>
          <EngageBar post={post} actions={actions} />
          {hidden ? null : <PostLinks post={post} actions={actions} />}
        </>
      )}
    </article>
  );
}
