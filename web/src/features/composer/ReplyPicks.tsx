// Replying to several posts at once (§10.1): ReplyPick, on every post the
// reader may reference, adds it to the reply; ReplyTray, mounted once in the
// shell, lists the picks and opens the composer with all of them.
// Owner: P0-W05 → P1-W12.
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import { MAX_PARENTS } from '../post/vm';
import { clearPicks, composeURL, togglePick, usePicks } from './picks';
import './picks.css';

// CLOSE is the remove button's decorative glyph (its name is the aria-label).
const CLOSE = '✕';

export function ReplyPick({ post }: { post: { ref: string; author: { handle: string } } }) {
  const { t } = useTranslation('composer');
  const picks = usePicks();
  const picked = picks.some((p) => p.ref === post.ref);
  if (!picked && picks.length >= MAX_PARENTS) return null;
  return (
    <button
      type="button"
      className="reply-pick"
      data-testid="reply-pick"
      aria-pressed={picked}
      onClick={() => {
        togglePick({ ref: post.ref, handle: post.author.handle });
      }}
    >
      {picked ? t('picks.picked') : t('picks.pick')}
    </button>
  );
}

export function ReplyTray() {
  const { t } = useTranslation('composer');
  const picks = usePicks();
  const navigate = useNavigate();
  if (picks.length === 0) return null;
  return (
    <section className="reply-tray" data-testid="reply-tray" aria-label={t('picks.label')}>
      <p className="reply-tray-title">{t('picks.title', { count: picks.length, max: MAX_PARENTS })}</p>
      <ul className="reply-tray-list">
        {picks.map((p) => (
          <li key={p.ref}>
            <span>{t('picks.handle', { handle: p.handle })}</span>
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
