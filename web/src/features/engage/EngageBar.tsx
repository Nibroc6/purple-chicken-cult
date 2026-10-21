// The like summary and the like / save / hide / show-less controls of one post. Pure
// over the view-model and PostActions (no API import), so any layout can use
// it. Once hidden in this session the post collapses to a notice.
// Owner: P0-W06 → P1-W08.
import { Fragment, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { errorKey } from '../../api/errors';
import type { PostActions } from '../../formats/types';
import type { LikesVM, PostVM } from '../../vm/types';
import { useFailed, useHidden } from './store';
import './engage.css';

// LikeSummary: "3 likes in your network", or "Liked by you, @bob and 2 others"
// when someone can be named (§5.3: only the viewer's own friends who allow it).
export function LikeSummary({ likes }: { likes: LikesVM }) {
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
        {t('likes.inNetwork', { count: likes.inNetwork })}
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
  if (likes.others > 0) items.push({ key: 'others', node: t('likes.others', { count: likes.others }) });
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
      <LikeSummary likes={post.likes} />
      <div className="engage-bar" role="group" aria-label={t('actions.label')}>
        <button
          type="button"
          data-testid="engage-like"
          aria-pressed={post.likes.byMe}
          onClick={() => {
            actions.like(post.ref, !post.likes.byMe);
          }}
        >
          {post.likes.byMe ? t('like.undo') : t('like.action')}
        </button>
        {actions.save ? (
          <button
            type="button"
            data-testid="engage-save"
            aria-pressed={post.savedByMe}
            title={t('save.private')}
            onClick={() => {
              actions.save?.(post.ref, !post.savedByMe);
            }}
          >
            {post.savedByMe ? t('save.undo') : t('save.action')}
          </button>
        ) : null}
        {post.mine ? null : (
          <>
            <button
              type="button"
              data-testid="engage-hide"
              onClick={() => {
                actions.hide(post.ref);
              }}
            >
              {t('hide.action')}
            </button>
            <button
              type="button"
              data-testid="engage-show-less"
              onClick={() => {
                actions.showLess(post.ref);
              }}
            >
              {t('showLess.action', { handle: post.author.handle })}
            </button>
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
