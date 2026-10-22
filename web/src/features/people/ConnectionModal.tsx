// "How you're connected" (?via=<post ref> over any page): the chain from the
// reader to a post's author step by step (§4.5), how many steps it is, and the
// author's inviter label with "report the inviter" (§3). A global slot; a
// post's chain opens it. Owner: P0-W03 → P1-W03.
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { Loading } from '../../app/Loading';
import { Modal, useModalParam } from '../../app/Modal';
import { ApiError, errorKey } from '../../api/errors';
import { InviterLabel } from '../../integrity';
import { chainSteps, viaParam } from '../../integrity/Chain';
import type { PostVM } from '../../vm/types';
import { fetchPost, postKey } from '../post/api';
import { useReport } from './shared';
import './people.css';

export function ConnectionModal() {
  const { t } = useTranslation('people');
  const [post, , close] = useModalParam(viaParam);
  if (!post) return null;
  return (
    <Modal title={t('via.title')} onClose={close} testId="via-modal">
      <ConnectionBody key={post} post={post} />
    </Modal>
  );
}

function ConnectionBody({ post }: { post: string }) {
  const { t } = useTranslation(['people', 'errors']);
  const q = useQuery({ queryKey: [...postKey, post], queryFn: () => fetchPost(post), retry: false });
  if (q.isPending) return <Loading />;
  if (q.error) {
    const missing = q.error instanceof ApiError && q.error.isNotFound;
    return (
      <p className="people-error" role="alert" data-testid="via-error">
        {missing ? t('via.missing') : t(errorKey(q.error) as 'errors:unknown')}
      </p>
    );
  }
  return <Connection post={q.data} />;
}

function Connection({ post }: { post: PostVM }) {
  const { t } = useTranslation(['people', 'integrity']);
  const report = useReport();
  const { author } = post;
  const tr = t as unknown as (k: string, o?: Record<string, unknown>) => string;
  const steps = chainSteps(author.chain, t('integrity:chain.handle', { handle: author.handle }), (k, o) => tr(`integrity:${k}`, o));
  const note = (s: (typeof steps)[number]): string | undefined => {
    if (s.kind === 'you') return undefined;
    if (s.kind === 'hidden') return t('via.hidden', { count: author.chain.hiddenMiddle });
    if (s.tie) return s.kind === 'author' ? `${t('via.tie', { tie: s.tie })}, ${t('via.author')}` : t('via.tie', { tie: s.tie });
    if (s.kind === 'author') return t('via.author');
    return t('via.named');
  };
  return (
    <div className="via" data-testid="via-body" data-dist={author.chain.dist}>
      {author.chain.dist > 0 ? (
        <p className="via-steps" data-testid="via-steps">
          {t('via.steps', { dist: author.chain.dist, handle: author.handle })}
        </p>
      ) : null}
      <ol className="via-path" data-testid="via-path">
        {steps.map((s) => (
          <li key={s.key} className="via-step" data-kind={s.kind} data-tie={s.tie}>
            <span className="via-node">{s.kind === 'you' ? t('via.you') : s.text}</span>
            {note(s) ? <span className="via-note muted">{note(s)}</span> : null}
          </li>
        ))}
      </ol>
      <p className="muted">{t('via.explain')}</p>
      <h3 className="via-heading">{t('via.invitedBy')}</h3>
      <p>
        <InviterLabel inviter={author.inviter} invitee={author.ref} onReport={report} />
      </p>
      <p>
        <Link to={`/people/${author.ref}`}>{t('via.person', { handle: author.handle })}</Link>
      </p>
    </div>
  );
}
