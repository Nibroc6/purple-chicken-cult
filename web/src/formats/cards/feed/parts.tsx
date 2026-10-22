// Building blocks of the Cards and Compact feed and post layouts (layout only:
// view-models and callbacks in, no API). Owner: P0-W07 → P1-W12.
//
// - SeenTrackProvider / useSeenTarget: one seen tracker per feed; only the
//   element of a card's own post carries data-seen-ref (§10.1.2). Context rows,
//   earlier thread parts and anything outside a feed never do.
// - FoldableBody: cards fold posts longer than 6 lines or 400 characters behind
//   "Show more" (the tree folds at 10 lines); a real button with aria-expanded
//   and aria-controls, so keyboard and screen-reader users unfold it too.
// - ContextRows, PersonLink, PostTime, PreviewList (with preview images),
//   MediaList (P1: attached pictures; display size in the feed and the focal
//   post, thumbs in tree rows), PostLinks.
// - groupTiers: feed items (across pages) → tier sections for sticky headers.
import { createContext, useContext, useId, useState, type MouseEvent, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useInRouterContext } from 'react-router';
import { IconButton } from '../../../lib/IconButton';
import { formatDateTime, formatRelative } from '../../../lib/time';
import { ReplyPick } from '../../../features/composer';
import { MediaImage } from '../../../features/media';
import { LinkPreviews, Markdown } from '../../../markdown';
import type { ContextVM, FeedItemVM, FeedPageVM, MarkdownDoc, MediaVM, PostVM, PreviewVM, Ref } from '../../../vm/types';
import type { PostActions } from '../../types';

// ---- seen tracking ----

type Track = (el: HTMLElement | null) => void;

const SeenTrack = createContext<Track | undefined>(undefined);

export function SeenTrackProvider({ track, children }: { track: Track | undefined; children: ReactNode }) {
  return <SeenTrack.Provider value={track}>{children}</SeenTrack.Provider>;
}

// useSeenTarget returns the attributes that make an element the seen target of
// post, or nothing when the element is not a feed card's own post.
export function useSeenTarget(post: Ref, own: boolean): { 'data-seen-ref'?: string; ref?: Track } {
  const track = useContext(SeenTrack);
  if (!own || !track) return {};
  return { 'data-seen-ref': post, ref: track };
}

// ---- folding (§10.1.5) ----

export type FoldAt = 'card' | 'tree';

// shouldFold: feed cards fold what the data layer marked; the tree folds at
// its own (longer) limit; the focal post never folds.
export function shouldFold(post: PostVM, variant: 'feed' | 'tree' | 'focal'): FoldAt | undefined {
  if (variant === 'focal' || post.fold === 'none') return undefined;
  if (variant === 'tree') return post.fold === 'tree' ? 'tree' : undefined;
  return 'card';
}

export function FoldableBody({ doc, fold }: { doc: MarkdownDoc; fold: FoldAt | undefined }) {
  const { t } = useTranslation('feed');
  const id = useId();
  const [open, setOpen] = useState(false);
  if (!fold) return <Markdown doc={doc} />;
  return (
    <div className="fold" data-testid="feed-fold" data-open={open}>
      <div id={id} className={open ? 'fold-body' : `fold-body fold-clamp fold-${fold}`}>
        <Markdown doc={doc} />
      </div>
      <button
        type="button"
        className="fold-toggle"
        data-testid="feed-fold-toggle"
        aria-expanded={open}
        aria-controls={id}
        onClick={() => {
          setOpen((o) => !o);
        }}
      >
        {open ? t('fold.less') : t('fold.more')}
      </button>
    </div>
  );
}

// ---- small pieces ----

// PersonLink links a person's page (a plain link outside a router, e.g. in a
// custom layout's preview).
export function PersonLink({ person, className, children }: { person: Ref; className?: string; children: ReactNode }) {
  const inRouter = useInRouterContext();
  const to = `/people/${person}`;
  return inRouter ? (
    <Link to={to} className={className}>
      {children}
    </Link>
  ) : (
    <a href={to} className={className}>
      {children}
    </a>
  );
}

// PostTime: when the post was written; given the post, also a link to it (the
// keyboard way to open a card, and the one that opens in a new tab).
export function PostTime({ at, post }: { at: Date; post?: Ref }) {
  const { i18n } = useTranslation();
  const inRouter = useInRouterContext();
  const time = (
    <time className="post-time muted" dateTime={at.toISOString()} title={formatDateTime(at, i18n.language)}>
      {formatRelative(at, i18n.language)}
    </time>
  );
  if (!post) return time;
  return inRouter ? (
    <Link to={postPath(post)} className="post-time-link" data-testid="post-time-link">
      {time}
    </Link>
  ) : (
    <a href={postPath(post)} className="post-time-link" data-testid="post-time-link">
      {time}
    </a>
  );
}

// ---- opening a card ----

export function postPath(post: Ref): string {
  return `/p/${post}`;
}

// What a click on a card leaves alone: its own controls and links, open
// panels, and media players.
const inner = 'a, button, input, textarea, select, label, summary, details, video, audio, [role="button"], [role="link"], [role="dialog"], [contenteditable="true"]';

// openCardProps makes a whole card open its post: a click anywhere but on its
// controls, links or a text selection opens it; Ctrl / Cmd / Shift-click and a
// middle click open it in a new tab. The time link (PostTime) is the keyboard
// path, so the card itself adds no tab stop.
export function openCardProps(post: Ref, open: (post: Ref) => void) {
  const go = (e: MouseEvent<HTMLElement>, newTab: boolean) => {
    const hit = (e.target as Element).closest(inner);
    if (hit && e.currentTarget.contains(hit)) return;
    if ((window.getSelection()?.toString() ?? '') !== '') return;
    if (newTab) window.open(postPath(post), '_blank', 'noopener');
    else open(post);
  };
  return {
    'data-open-card': 'true',
    onClick: (e: MouseEvent<HTMLElement>) => {
      if (e.button === 0) go(e, e.metaKey || e.ctrlKey || e.shiftKey);
    },
    onAuxClick: (e: MouseEvent<HTMLElement>) => {
      if (e.button === 1) go(e, true);
    },
  };
}

// ContextRows: "↳ replying to @a: first 80 characters…", only for links the
// server says the reader may see (§10.1.2). Rendering a row never marks its
// post as seen. Continuation rows are left out where the thread is stacked.
// i18n-dynamic: feed:context.
export function ContextRows({ rows, withCont = true }: { rows: ContextVM[]; withCont?: boolean }) {
  const { t } = useTranslation('feed');
  const shown = withCont ? rows : rows.filter((r) => r.kind === 'ref');
  if (shown.length === 0) return null;
  return (
    <ul className="feed-context" aria-label={t('context.label')}>
      {shown.map((c) => (
        <li key={c.ref} className="feed-context-row muted" data-testid="feed-context">
          {t(`context.${c.kind}` as 'context.ref', { handle: c.authorHandle })} <span data-user-content="">{c.excerpt}</span>
        </li>
      ))}
    </ul>
  );
}

// PreviewList shows link previews with their real host (§10.1.6) through the
// shared Markdown module (protocol check, tracking-parameter stripping), then
// the previews' images (fetched and scanned by the server, never by viewers).
export function PreviewList({ previews }: { previews: readonly PreviewVM[] }) {
  const images = previews.flatMap((p) => (p.image ? [p.image] : []));
  return (
    <>
      <LinkPreviews previews={previews} />
      {images.length > 0 ? (
        <div className="post-preview-images" data-testid="feed-preview-images">
          {images.map((m) => (
            <MediaImage key={m.ref} media={m} variant="thumb" />
          ))}
        </div>
      ) : null}
    </>
  );
}

// mediaVariant: tree rows show thumbs; feed cards and the focal post the
// display size.
export function mediaVariant(variant: 'feed' | 'tree' | 'focal'): 'thumb' | 'display' {
  return variant === 'tree' ? 'thumb' : 'display';
}

// MediaList shows a post's attached pictures in order, each with its alt text.
export function MediaList({ media, variant }: { media: readonly MediaVM[]; variant: 'thumb' | 'display' }) {
  if (media.length === 0) return null;
  return (
    <div className={`post-media post-media-${variant}`} data-testid="feed-media" data-count={media.length}>
      {media.map((m) => (
        <MediaImage key={m.ref} media={m} variant={variant} />
      ))}
    </div>
  );
}

// PostLinks: reply and add to a reply to several posts (when the reader may
// reference the post), and the family tree with its visible reply count.
export function PostLinks({ post, actions }: { post: PostVM; actions: PostActions }) {
  const { t, i18n } = useTranslation('feed');
  return (
    <div className="post-links" role="group" aria-label={t('post.links')}>
      {post.canReference ? (
        <IconButton
          icon="reply"
          label={t('post.reply')}
          data-testid="feed-reply"
          data-part="reply"
          onClick={() => {
            actions.reply(post.ref);
          }}
        />
      ) : null}
      {post.canReference ? <ReplyPick post={post} /> : null}
      <IconButton
        icon="replies"
        label={t('post.tree', { count: post.replyCount })}
        data-testid="feed-tree"
        data-part="tree"
        onClick={() => {
          actions.openTree(post.ref);
        }}
      >
        {post.replyCount > 0 ? <span className="icon-button-count">{new Intl.NumberFormat(i18n.language).format(post.replyCount)}</span> : null}
      </IconButton>
    </div>
  );
}

// ---- feed structure ----

export interface TierSection {
  key: string;
  tier: number; // 0 for items outside any tier (caught up)
  start: boolean; // the API sent tier_start for it
  items: FeedItemVM[]; // posts, threads, collapsed groups
  end: boolean;
}

// groupTiers turns the items of every page into tier sections. Headers come
// only from tier_start / tier_end / caught_up markers (§10.1.7), never from
// post content; a tier continued on a later page stays one section.
export function groupTiers(pages: FeedPageVM[]): { sections: TierSection[]; caughtUp: boolean } {
  const sections: TierSection[] = [];
  let cur: TierSection | undefined;
  let caughtUp = false;
  let n = 0;
  for (const p of pages) {
    for (const it of p.items) {
      switch (it.type) {
        case 'tier_start':
          cur = { key: `t${String(n++)}`, tier: it.tier, start: true, items: [], end: false };
          sections.push(cur);
          break;
        case 'tier_end':
          if (cur?.tier === it.tier) cur.end = true;
          else sections.push({ key: `t${String(n++)}`, tier: it.tier, start: false, items: [], end: true });
          cur = undefined;
          break;
        case 'caught_up':
          caughtUp = true;
          cur = undefined;
          break;
        default:
          if (!cur) {
            cur = { key: `t${String(n++)}`, tier: 0, start: false, items: [], end: false };
            sections.push(cur);
          }
          cur.items.push(it);
      }
    }
  }
  return { sections, caughtUp };
}

// itemKey is a stable key for a feed item.
export function itemKey(it: FeedItemVM, i: number): string {
  switch (it.type) {
    case 'post':
      return it.post.ref;
    case 'thread':
      return it.parts[it.parts.length - 1]?.ref ?? `thread-${String(i)}`;
    case 'collapsed':
      return it.token;
    default:
      return `${it.type}-${String(i)}`;
  }
}
