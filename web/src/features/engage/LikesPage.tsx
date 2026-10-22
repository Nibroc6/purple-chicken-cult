// /p/:ref/likes: one post with its in-network like summary and the like, hide
// and show-less controls. The post's own card is seen-tracked (data-seen-ref);
// its context rows are not. Owner: P0-W06 → P1-W08.
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Link, useParams } from 'react-router';
import { Loading } from '../../app/Loading';
import { NotFound } from '../../app/NotFound';
import { ApiError, errorText } from '../../api/errors';
import { fetchPost, postKey } from '../post/api';
import { PostPreview } from '../post/shared';
import { useSeenTracker } from '../seen/useSeenTracker';
import { EngageBar } from './EngageBar';
import { usePostActions } from './usePostActions';
import './engage.css';
import { InfoTip } from '../../lib/InfoTip';

export default function LikesPage() {
  const { t } = useTranslation(['engage', 'errors']);
  const ref = useParams().ref ?? '';
  const q = useQuery({ queryKey: [...postKey, ref], queryFn: () => fetchPost(ref), retry: false });
  const actions = usePostActions();
  const track = useSeenTracker();
  if (q.error instanceof ApiError && q.error.isNotFound) return <NotFound />;
  return (
    <section className="page engage-page">
      <h1>{t('page.title')}</h1>
      {q.isPending ? <Loading /> : null}
      {q.error ? (
        <p className="engage-error" role="alert">
          {errorText(t, q.error)}
        </p>
      ) : null}
      {q.data ? (
        <>
          <div data-seen-ref={q.data.ref} ref={track} data-testid="engage-card">
            <PostPreview post={q.data} />
          </div>
          <EngageBar post={q.data} actions={actions} />
          <p className="muted">
            {t('likes.explainShort')}
            <InfoTip>{t('likes.explain')}</InfoTip>
          </p>
          <p className="post-links">
            <Link to={`/p/${q.data.ref}`}>{t('page.open')}</Link> <Link to="/seen">{t('page.seen')}</Link>
          </p>
        </>
      ) : null}
    </section>
  );
}
