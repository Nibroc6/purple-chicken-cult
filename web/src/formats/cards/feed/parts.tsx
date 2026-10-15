// Building blocks of the Cards and Compact feed and post layouts (layout only:
// view-models and callbacks in, no API). Owner: P0-W07 → P1-W12.
//
// - SeenTrackProvider / useSeenTarget: one seen tracker per feed; only the
//   element of a card's own post carries data-seen-ref (§10.1.2). Context rows,
//   earlier thread parts and anything outside a feed never do.
// - FoldableBody: cards fold posts longer than 6 lines or 400 characters behind
//   "Show more" (the tree folds at 10 lines); a real button with aria-expanded
//   and aria-controls, so keyboard and screen-reader users unfold it too.
// - ContextRows, PersonLink, PostTime, PreviewList, PostLinks.
// - groupTiers: feed items (across pages) → tier sections for sticky headers.
import { createContext, useContext, useId, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useInRouterContext } from 'react-router';
import { formatDateTime, formatRelative } from '../../../lib/time';
import { Markdown } from '../../../markdown';
import type { ContextVM, FeedItemVM, FeedPageVM, MarkdownDoc, PostVM, PreviewVM, Ref } from '../../../vm/types';
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

export function PostTime({ at }: { at: Date }) {
  const { i18n } = useTranslation();
  return (
    <time className="post-time muted" dateTime={at.toISOString()} title={formatDateTime(at, i18n.language)}>
      {formatRelative(at, i18n.language)}
    </time>
  );
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

function safeHref(url: string): string | undefined {
  try {
    const u = new URL(url);
    return u.protocol === 'http:' || u.protocol === 'https:' ? u.href : undefined;
  } catch {
    return undefined;
  }
}

// PreviewList shows link previews with their real host (§10.1.6); an href is
// rendered only for http(s).
export function PreviewList({ previews }: { previews: PreviewVM[] }) {
  const { t } = useTranslation('feed');
  if (previews.length === 0) return null;
  return (
    <ul className="feed-previews">
      {previews.map((p) => {
        const href = safeHref(p.url);
        const host = (
          <span className="preview-host" data-lookalike={p.lookalike}>
            {p.lookalike ? t('preview.lookalike', { host: p.hostUnicode }) : p.hostUnicode}
          </span>
        );
        return (
          <li key={p.url} className="feed-preview" data-testid="feed-preview">
            {href ? (
              <a href={href} rel="noopener noreferrer nofollow ugc" referrerPolicy="no-referrer" target="_blank">
                {host}
              </a>
            ) : (
              host
            )}
            {p.title ? (
              <strong className="preview-title" data-user-content="">
                {p.title}
              </strong>
            ) : null}
            {p.description ? (
              <span className="preview-description muted" data-user-content="">
                {p.description}
              </span>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}

// PostLinks: reply (when the reader may reference the post) and the family tree
// with its visible reply count.
export function PostLinks({ post, actions }: { post: PostVM; actions: PostActions }) {
  const { t } = useTranslation('feed');
  return (
    <div className="post-links" role="group" aria-label={t('post.links')}>
      {post.canReference ? (
        <button
          type="button"
          data-testid="feed-reply"
          onClick={() => {
            actions.reply(post.ref);
          }}
        >
          {t('post.reply')}
        </button>
      ) : null}
      <button
        type="button"
        data-testid="feed-tree"
        onClick={() => {
          actions.openTree(post.ref);
        }}
      >
        {t('post.tree', { count: post.replyCount })}
      </button>
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
