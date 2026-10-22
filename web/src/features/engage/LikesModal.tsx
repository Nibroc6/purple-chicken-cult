// The likes modal (?likes=<post ref> over any page): who in the viewer's
// network liked the post, as far as they can be named (§5.3), and the like
// button. A global slot, so every layout's like summary can open it.
// /p/:ref/likes stays as a full page for old links. Owner: P1-W08.
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { Loading } from '../../app/Loading';
import { Modal, useModalParam } from '../../app/Modal';
import { ApiError, errorKey } from '../../api/errors';
import { asRef, type Ref } from '../../vm/types';
import { fetchLikes, likesKey } from './api';
import { likesParam } from './EngageBar';
import { useFailed } from './store';
import { usePostActions } from './usePostActions';
import './engage.css';

export function LikesModal() {
  const { t } = useTranslation('engage');
  const [post, , close] = useModalParam(likesParam);
  if (!post) return null;
  return (
    <Modal title={t('modal.title')} onClose={close} testId="likes-modal">
      <LikesBody key={post} post={asRef(post)} />
    </Modal>
  );
}

function LikesBody({ post }: { post: Ref }) {
  const { t } = useTranslation(['engage', 'errors']);
  const q = useQuery({ queryKey: [...likesKey, post], queryFn: () => fetchLikes(post), retry: false });
  const actions = usePostActions();
  const failed = useFailed(post);
  if (q.isPending) return <Loading />;
  if (q.error) {
    const missing = q.error instanceof ApiError && q.error.isNotFound;
    return (
      <p className="engage-error" role="alert" data-testid="likes-modal-error">
        {missing ? t('modal.missing') : t(errorKey(q.error) as 'errors:unknown')}
      </p>
    );
  }
  const likes = q.data;
  return (
    <>
      <p data-testid="likes-modal-count" data-count={likes.inNetwork}>
        {likes.inNetwork === 0 ? t('likes.none') : t('likes.inNetwork', { count: likes.inNetwork })}
      </p>
      {likes.byMe || likes.names.length > 0 || likes.others > 0 ? (
        <ul className="likes-modal-list" data-testid="likes-modal-list">
          {likes.byMe ? <li>{t('modal.you')}</li> : null}
          {likes.names.map((n) => (
            <li key={n.ref}>
              <Link to={`/people/${n.ref}`} data-testid="likes-modal-liker">
                @{n.handle}
              </Link>
            </li>
          ))}
          {likes.others > 0 ? <li className="muted">{t('modal.others', { count: likes.others })}</li> : null}
        </ul>
      ) : null}
      <p className="muted">{t('likes.explain')}</p>
      <div className="likes-modal-actions">
        <button
          type="button"
          data-testid="likes-modal-like"
          aria-pressed={likes.byMe}
          onClick={() => {
            actions.like(post, !likes.byMe);
          }}
        >
          {likes.byMe ? t('like.undo') : t('like.action')}
        </button>
        <Link to={`/p/${post}`}>{t('modal.open')}</Link>
      </div>
      {failed ? (
        <p className="engage-error" role="alert">
          {t(errorKey(failed) as 'errors:unknown')}
        </p>
      ) : null}
    </>
  );
}
