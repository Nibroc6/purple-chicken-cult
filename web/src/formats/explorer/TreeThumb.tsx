// The small tree thumbnail of an Explorer feed card (§15.1): the post's
// parents above it and its replies below, drawn from the card's own view-model
// (the posts it replies to among its context rows, its visible reply count),
// as a button that opens the family tree. Owner: P1-W09.
import { useTranslation } from 'react-i18next';
import type { PostVM } from '../../vm/types';
import type { PostActions } from '../types';

const MAX_DOTS = 5;
const W = 72;
const H = 40;

function dots(n: number, y: number) {
  const k = Math.min(n, MAX_DOTS);
  return Array.from({ length: k }, (_, i) => ({ x: W / 2 + (i - (k - 1) / 2) * 12, y }));
}

export function TreeThumb({ post, actions }: { post: PostVM; actions: PostActions }) {
  const { t } = useTranslation('explorer');
  const parents = post.context.filter((c) => c.kind === 'ref').length;
  const replies = post.replyCount;
  const up = dots(parents, 6);
  const down = dots(replies, H - 6);
  const mid = { x: W / 2, y: H / 2 };
  return (
    <button
      type="button"
      className="explorer-thumb"
      data-testid="explorer-thumb"
      data-parents={parents}
      data-replies={replies}
      aria-label={t('thumb.label', { parents, replies })}
      onClick={() => {
        actions.openTree(post.ref);
      }}
    >
      <svg viewBox={`0 0 ${String(W)} ${String(H)}`} width={W} height={H} aria-hidden="true">
        {[...up, ...down].map((d, i) => (
          <line key={`l${String(i)}`} className="explorer-thumb-edge" x1={mid.x} y1={mid.y} x2={d.x} y2={d.y} />
        ))}
        {[...up, ...down].map((d, i) => (
          <circle key={`d${String(i)}`} className="explorer-thumb-dot" cx={d.x} cy={d.y} r={3.5} />
        ))}
        <circle className="explorer-thumb-self" cx={mid.x} cy={mid.y} r={5} />
        {replies > MAX_DOTS ? (
          <text className="explorer-thumb-more" x={W - 2} y={H - 3} textAnchor="end">
            {t('thumb.more')}
          </text>
        ) : null}
      </svg>
    </button>
  );
}
