// Replying to several posts at once (§10.1): ReplyPick, on every post the
// reader may reference, adds it to the reply; ReplyTray, mounted once in the
// shell, lists the picks and opens the composer with all of them.
// Owner: P0-W05 → P1-W12.
import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import { excerpt } from '../../formats/tree/canvas/layout';
import { IconButton } from '../../lib/IconButton';
import { MAX_PARENTS } from '../post/vm';
import { clearPicks, composeURL, togglePick, usePicks, type Pick } from './picks';
import './picks.css';

// CLOSE is the remove button's decorative glyph (its name is the aria-label).
const CLOSE = '✕';

// WIDE matches screens where the tray shows the picked posts' cards from the
// start; on phones they fold behind a Show button (picks.css).
const WIDE = '(min-width: 48rem)';

interface PickablePost {
  ref: string;
  author: { handle: string; displayName?: string };
  body?: { nodes?: readonly unknown[] } | null;
}

function toPick(post: PickablePost): Pick {
  const text = excerpt(post.body?.nodes);
  return {
    ref: post.ref,
    handle: post.author.handle,
    ...(post.author.displayName ? { name: post.author.displayName } : {}),
    ...(text ? { excerpt: text } : {}),
  };
}

export function ReplyPick({ post }: { post: PickablePost }) {
  const { t } = useTranslation('composer');
  const picks = usePicks();
  const picked = picks.some((p) => p.ref === post.ref);
  if (!picked && picks.length >= MAX_PARENTS) return null;
  return (
    <IconButton
      icon="reply-pick"
      label={picked ? t('picks.picked') : t('picks.pick')}
      className="reply-pick"
      data-testid="reply-pick"
      data-part="reply-pick"
      aria-pressed={picked}
      onClick={() => {
        togglePick(toPick(post));
      }}
    />
  );
}

// PickedBadge marks a post that is in the reader's multi-post reply, on the
// card itself, with a button to take it out again.
export function PickedBadge({ post }: { post: PickablePost }) {
  const { t } = useTranslation('composer');
  const picks = usePicks();
  const i = picks.findIndex((p) => p.ref === post.ref);
  if (i < 0) return null;
  return (
    <span className="reply-picked" data-testid="reply-picked">
      <span>{t('picks.badge', { n: i + 1, count: picks.length })}</span>
      <button
        type="button"
        className="reply-picked-remove"
        data-testid="reply-picked-remove"
        aria-label={t('picks.remove', { handle: post.author.handle })}
        onClick={() => {
          togglePick({ ref: post.ref, handle: post.author.handle });
        }}
      >
        <span aria-hidden="true">{CLOSE}</span>
      </button>
    </span>
  );
}

export function ReplyTray() {
  const { t } = useTranslation('composer');
  const picks = usePicks();
  const navigate = useNavigate();
  const uid = useId().replace(/:/g, '');
  // Phones start with the cards folded so the tray stays one slim bar.
  const [open, setOpen] = useState(() => typeof window.matchMedia !== 'function' || window.matchMedia(WIDE).matches);
  if (picks.length === 0) return null;
  return (
    <section className="reply-tray" data-testid="reply-tray" data-open={open} aria-label={t('picks.label')}>
      <div className="reply-tray-head">
        <p className="reply-tray-title">
          {t('picks.title', { count: picks.length })}
          <span className="reply-tray-hint muted"> {t('picks.hint', { max: MAX_PARENTS })}</span>
        </p>
        <button
          type="button"
          className="reply-tray-toggle"
          data-testid="reply-tray-toggle"
          aria-expanded={open}
          aria-controls={`${uid}-list`}
          onClick={() => {
            setOpen((o) => !o);
          }}
        >
          {open ? t('picks.hide') : t('picks.show')}
        </button>
      </div>
      <ul id={`${uid}-list`} className="reply-tray-list">
        {picks.map((p) => (
          <li key={p.ref} className="reply-tray-card" data-testid="reply-tray-card">
            <span className="reply-tray-card-text">
              <span className="reply-tray-card-who">
                {p.name ? <strong data-user-content="">{p.name}</strong> : null}{' '}
                <span className="muted">{t('picks.handle', { handle: p.handle })}</span>
              </span>
              {p.excerpt ? (
                <span className="reply-tray-card-excerpt" data-user-content="">
                  {p.excerpt}
                </span>
              ) : null}
            </span>
            <button
              type="button"
              className="reply-tray-remove"
              data-testid="reply-tray-remove"
              aria-label={t('picks.remove', { handle: p.handle })}
              onClick={() => {
                togglePick(p);
              }}
            >
              <span aria-hidden="true">{CLOSE}</span>
            </button>
          </li>
        ))}
      </ul>
      <div className="reply-tray-actions">
        <button
          type="button"
          className="primary"
          data-testid="reply-tray-write"
          onClick={() => {
            const url = composeURL(picks.map((p) => p.ref));
            clearPicks();
            void navigate(url);
          }}
        >
          {t('picks.write')}
        </button>
        <button
          type="button"
          data-testid="reply-tray-clear"
          onClick={() => {
            clearPicks();
          }}
        >
          {t('picks.clear')}
        </button>
      </div>
    </section>
  );
}
