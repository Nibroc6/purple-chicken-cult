// The like summary and the like / save / hide / show-less controls of one post. Pure
// over the view-model and PostActions (no API import), so any layout can use
// it. Once hidden in this session the post collapses to a notice. Each control
// is an icon button with a tooltip; data-part names it for theme CSS
// (features/themes/sheet.ts).
// Owner: P0-W06 → P1-W08.
import { Fragment, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useInRouterContext } from 'react-router';
import { errorKey } from '../../api/errors';
import { useModalParam } from '../../app/Modal';
import { IconButton } from '../../lib/IconButton';
import type { PostActions } from '../../formats/types';
import type { LikesVM, PostVM, Ref } from '../../vm/types';
import { useFailed, useHidden } from './store';
import './engage.css';

// likesParam is the search parameter of the likes modal (?likes=<post ref>).
export const likesParam = 'likes';

// useOpenLikes opens the likes modal of a post over the current page.
export function useOpenLikes(): (post: Ref) => void {
  const [, open] = useModalParam(likesParam);
  return open;
}

// OpenLikes is the part of the summary that opens the likes modal: a button
// inside a router, plain text outside one (a layout preview).
function OpenLikes({ post, children }: { post?: Ref; children: ReactNode }) {
  const inRouter = useInRouterContext();
  if (!post || !inRouter) return <>{children}</>;
  return <OpenLikesButton post={post}>{children}</OpenLikesButton>;
}

function OpenLikesButton({ post, children }: { post: Ref; children: ReactNode }) {
  const open = useOpenLikes();
  return (
    <button
      type="button"
      className="engage-likes-open"
      data-testid="engage-likes-open"
      onClick={() => {
        open(post);
      }}
    >
      {children}
    </button>
  );
}

// LikeSummary: "3 likes in your network", or "Liked by you, @bob and 2 others"
// when someone can be named (§5.3: only the viewer's own friends who allow it).
// Given the post, the count opens the likes modal.
export function LikeSummary({ likes, post }: { likes: LikesVM; post?: Ref }) {
  const { t, i18n } = useTranslation('engage');
  if (likes.inNetwork === 0) {
    return (
      <p className="engage-likes muted" data-testid="engage-likes" data-count={0}>
        {t('likes.none')}
      </p>
    );
  }
  if (likes.names.length === 0 && !likes.byMe) {
    return (
      <p className="engage-likes" data-testid="engage-likes" data-count={likes.inNetwork}>
        <OpenLikes post={post}>{t('likes.inNetwork', { count: likes.inNetwork })}</OpenLikes>
      </p>
    );
  }
  type Item = { key: string; node: ReactNode };
  const items: Item[] = [];
  if (likes.byMe) items.push({ key: 'me', node: t('likes.you') });
  for (const n of likes.names) {
    items.push({
      key: n.ref,
      node: (
        <Link to={`/people/${n.ref}`} data-testid="engage-liker">
          @{n.handle}
        </Link>
      ),
    });
  }
  if (likes.others > 0) {
    items.push({ key: 'others', node: <OpenLikes post={post}>{t('likes.others', { count: likes.others })}</OpenLikes> });
  }
  // Intl.ListFormat places the separators ("a, b and c") for the UI locale.
  const parts = new Intl.ListFormat(i18n.language, { type: 'conjunction' }).formatToParts(items.map((it) => it.key));
  let i = 0;
  return (
    <p className="engage-likes" data-testid="engage-likes" data-count={likes.inNetwork}>
      {t('likes.likedBy')}{' '}
      {parts.map((p, k) => {
        if (p.type === 'literal') return <Fragment key={`l${String(k)}`}>{p.value}</Fragment>;
        const it = items[i++];
        return <Fragment key={it?.key ?? k}>{it?.node}</Fragment>;
      })}
    </p>
  );
}

export function EngageBar({ post, actions }: { post: PostVM; actions: PostActions }) {
  const { t } = useTranslation(['engage', 'errors']);
  const hidden = useHidden(post.ref);
  const failed = useFailed(post.ref);
  if (hidden) {
    return (
      <p className="engage-hidden muted" role="status" data-testid="engage-hidden" data-kind={hidden}>
        {hidden === 'show_less' ? t('showLess.done', { handle: post.author.handle }) : t('hide.done')}
      </p>
    );
  }
  return (
    <div className="engage" data-testid="engage">
      <LikeSummary likes={post.likes} post={post.ref} />
      <div className="engage-bar" role="group" aria-label={t('actions.label')}>
        <IconButton
          icon={post.likes.byMe ? 'like-on' : 'like'}
          label={post.likes.byMe ? t('like.undo') : t('like.action')}
          data-testid="engage-like"
          data-part="like"
          aria-pressed={post.likes.byMe}
          onClick={() => {
            actions.like(post.ref, !post.likes.byMe);
          }}
        />
        {actions.save ? (
          <IconButton
            icon={post.savedByMe ? 'save-on' : 'save'}
            label={post.savedByMe ? t('save.undo') : t('save.action')}
            hint={t('save.private')}
            data-testid="engage-save"
            data-part="save"
            aria-pressed={post.savedByMe}
            onClick={() => {
              actions.save?.(post.ref, !post.savedByMe);
            }}
          />
        ) : null}
        {post.mine ? null : (
          <>
            <IconButton
              icon="hide"
              label={t('hide.action')}
              data-testid="engage-hide"
              data-part="hide"
              onClick={() => {
                actions.hide(post.ref);
              }}
            />
            <IconButton
              icon="show-less"
              label={t('showLess.action', { handle: post.author.handle })}
              data-testid="engage-show-less"
              data-part="show-less"
              onClick={() => {
                actions.showLess(post.ref);
              }}
            />
          </>
        )}
      </div>
      {failed ? (
        <p className="engage-error" role="alert" data-testid="engage-error">
          {t(errorKey(failed) as 'errors:unknown')}
        </p>
      ) : null}
    </div>
  );
}
