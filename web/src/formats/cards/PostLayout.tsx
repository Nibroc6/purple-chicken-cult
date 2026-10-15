// Cards (default) post layout (SCAFFOLD hand-off stub). Owner: P0-W07 → P1-W12.
// Renders every integrity element; real layout comes with the owning WP.
import { AudienceBadge, Chain, EditedMark, InviterLabel, ReportAction, WhyButton } from '../../integrity';
import { Markdown } from '../../markdown';
import type { PostVM } from '../../vm/types';
import type { PostActions } from '../types';

export function PostLayout({ post, actions }: { post: PostVM; actions: PostActions; variant: 'feed' | 'tree' | 'focal' }) {
  return (
    <article className="post" data-format="cards">
      <header className="post-author">
        <span className="handle">@{post.author.handle}</span>
        <InviterLabel inviter={post.author.inviter} invitee={post.author.ref} onReport={actions.report} />
        <Chain chain={post.author.chain} author={post.author} />
        <WhyButton post={post.ref} />
      </header>
      {post.body ? <Markdown doc={post.body} /> : null}
      <footer className="post-meta">
        <AudienceBadge audience={post.audience} forced={post.audienceForced} />
        <EditedMark {...(post.editedAt ? { editedAt: post.editedAt } : {})} />
        <ReportAction target={{ kind: 'post', ref: post.ref }} onReport={actions.report} />
      </footer>
    </article>
  );
}
