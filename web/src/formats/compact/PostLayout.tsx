// Compact post layout: one dense row per post — a single author line
// (@handle, display name, inviter label, chain, time, "why"), the visible
// reply context, the folded body, then the integrity footer and the actions on
// one line. Owner: P0-W07 → P1-W12 (P0-W08 reuses it for the tree outline).
import { useTranslation } from 'react-i18next';
import { EngageBar, useHidden } from '../../features/engage';
import { AudienceBadge, Chain, EditedMark, InviterLabel, ReportAction, WhyButton } from '../../integrity';
import type { PostVM } from '../../vm/types';
import type { PostActions } from '../types';
import {
  ContextRows,
  FoldableBody,
  PersonLink,
  PostLinks,
  PostTime,
  PreviewList,
  shouldFold,
  useSeenTarget,
} from '../cards/feed/parts';
import './feed/compact.css';

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
  const { t } = useTranslation('feed');
  const hidden = useHidden(post.ref);
  const seen = useSeenTarget(post.ref, variant === 'feed' && part !== 'context' && !hidden);
  return (
    <article
      className={`post post-row post-${variant}`}
      data-format="compact"
      data-testid="feed-post"
      data-ref={post.ref}
      data-tier={post.tier}
      data-part={part}
      {...seen}
    >
      <header className="row-head">
        <PersonLink person={post.author.ref} className="post-who">
          <span className="post-handle">{t('post.handle', { handle: post.author.handle })}</span>
          {post.author.displayName ? <span className="post-name muted"> {post.author.displayName}</span> : null}
        </PersonLink>
        <InviterLabel inviter={post.author.inviter} invitee={post.author.ref} onReport={actions.report} />
        <Chain chain={post.author.chain} author={post.author} />
        <PostTime at={post.createdAt} />
        {post.thread ? (
          <span className="post-thread muted" data-testid="feed-thread-pos">
            {t('thread.position', { index: post.thread.index, total: post.thread.total })}
          </span>
        ) : null}
        <WhyButton post={post.ref} />
      </header>
      {hidden ? null : (
        <div className="row-body">
          <ContextRows rows={post.context} withCont={part === undefined} />
          {post.body ? <FoldableBody doc={post.body} fold={shouldFold(post, variant)} /> : null}
          <PreviewList previews={post.previews} />
        </div>
      )}
      <footer className="row-foot">
        <AudienceBadge audience={post.audience} forced={post.audienceForced} />
        <EditedMark {...(post.editedAt ? { editedAt: post.editedAt } : {})} />
        <ReportAction target={{ kind: 'post', ref: post.ref }} onReport={actions.report} />
        {part === 'context' || hidden ? null : <PostLinks post={post} actions={actions} />}
      </footer>
      {part === 'context' ? null : <EngageBar post={post} actions={actions} />}
    </article>
  );
}
