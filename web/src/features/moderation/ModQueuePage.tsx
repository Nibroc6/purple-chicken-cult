// Moderator queue (/mod; §8, §10.4). Owner: P0-W12 → P1-W06. Moderators see the
// reported content (never for csam), the subject's handle, inviter label and
// probation flag, and nothing else of the graph. remove_post and dismiss take
// one moderator; suspend_user needs a passkey confirmation and then a second,
// different moderator (also freshly confirmed) within 24 hours. A 401
// step_up_required opens the global passkey step-up; the moderator then retries.
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { errorKey } from '../../api/errors';
import { Loading } from '../../app/Loading';
import { useMe } from '../../app/session';
import { formatDateTime, formatRelative } from '../../lib/time';
import {
  actions,
  confirmItem,
  errorParams,
  fetchQueue,
  modReasons,
  moderationErrorKey,
  resolveItem,
  type Action,
  type ModItem,
  type ModReason,
} from './api';
import './moderation.css';

export const queueKey = ['moderation', 'queue'] as const;

export default function ModQueuePage() {
  const { t } = useTranslation('moderation');
  const me = useMe();
  const moderator = me?.role === 'moderator' || me?.role === 'admin';
  return (
    <section className="page moderation-page" data-testid="mod-queue-page">
      <h1>{t('queue.title')}</h1>
      {moderator ? <Queue /> : <p className="muted">{t('queue.membersOnly')}</p>}
    </section>
  );
}

function Queue() {
  const { t } = useTranslation(['moderation', 'errors']);
  const q = useInfiniteQuery({
    queryKey: queueKey,
    queryFn: ({ pageParam }) => fetchQueue(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next,
    retry: false,
  });
  const items = q.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <>
      <p className="muted">{t('queue.intro')}</p>
      <ul className="link-list">
        <li>
          <Link to="/mod/log">{t('footer.log')}</Link>
        </li>
        <li>
          <Link to="/mod/recovery">{t('footer.recovery')}</Link>
        </li>
      </ul>
      {q.isPending ? <Loading /> : null}
      {q.error ? (
        <p className="moderation-error" role="alert">
          {t(errorKey(q.error) as 'errors:unknown')}
        </p>
      ) : null}
      {q.isSuccess && items.length === 0 ? (
        <p className="muted" data-testid="queue-empty">
          {t('queue.empty')}
        </p>
      ) : null}
      <ol className="moderation-list" data-testid="queue-list">
        {items.map((it) => (
          <QueueItem key={it.id} it={it} />
        ))}
      </ol>
      {q.hasNextPage ? (
        <button
          type="button"
          disabled={q.isFetchingNextPage}
          onClick={() => {
            void q.fetchNextPage();
          }}
        >
          {t('queue.more')}
        </button>
      ) : null}
    </>
  );
}

// hasPost: remove_post applies to post and preview reports only.
function hasPost(it: ModItem): boolean {
  return it.post !== undefined || (it.summary?.kind_post ?? 0) > 0 || (it.summary?.kind_preview ?? 0) > 0;
}

function initialReason(r: string): ModReason {
  return (modReasons as readonly string[]).includes(r) ? (r as ModReason) : 'other';
}

function QueueItem({ it }: { it: ModItem }) {
  const { t, i18n } = useTranslation(['moderation', 'errors']);
  const qc = useQueryClient();
  const id = useId();
  const allowed = actions.filter((a) => a !== 'remove_post' || hasPost(it));
  const [action, setAction] = useState<Action>(allowed[0] ?? 'dismiss');
  const [reason, setReason] = useState<ModReason>(initialReason(it.reason));
  const done = () => qc.invalidateQueries({ queryKey: queueKey });
  const resolve = useMutation({ mutationFn: () => resolveItem(it.id, action, reason), onSuccess: done });
  const confirm = useMutation({ mutationFn: () => confirmItem(it.id), onSuccess: done });
  const err = resolve.error ?? confirm.error;
  const at = new Date(it.at);
  const reports = it.summary?.reports ?? 1;
  return (
    <li className="moderation-item" data-testid="queue-item" data-reason={it.reason} data-awaiting={it.awaiting_second}>
      <p className="moderation-text">
        {/* i18n-dynamic: moderation:reason. */}
        <strong>{t(`reason.${it.reason}` as 'reason.other')}</strong>
        {it.priority === 0 ? <span className="moderation-tag">{t('queue.urgent')}</span> : null}{' '}
        <span className="muted">{t('queue.reports', { count: reports })}</span>
      </p>
      {it.subject ? (
        <p className="moderation-meta" data-testid="queue-subject">
          {t('queue.subject', { handle: it.subject.handle })}{' '}
          {it.subject_inviter ? (
            <span className="muted" data-testid="queue-inviter">
              {t('queue.inviter', { kind: it.subject_inviter.kind, handle: it.subject_inviter.handle ?? '' })}
            </span>
          ) : null}
          {it.subject_probation ? (
            <span className="moderation-tag" data-testid="queue-probation">
              {t('queue.probation')}
            </span>
          ) : null}
        </p>
      ) : null}
      <p className="moderation-meta muted">
        <time dateTime={at.toISOString()} title={formatDateTime(at, i18n.language)}>
          {formatRelative(at, i18n.language)}
        </time>
      </p>
      {it.post ? (
        <blockquote className="moderation-post" data-user-content="" data-testid="queue-post">
          {it.post.body}
        </blockquote>
      ) : it.reason === 'csam' ? (
        <p className="muted">{t('queue.noContent')}</p>
      ) : null}
      {it.awaiting_second ? (
        <div className="moderation-actions">
          <p role="status">{t('queue.awaiting')}</p>
          <button
            type="button"
            disabled={confirm.isPending}
            data-testid="queue-confirm"
            onClick={() => {
              confirm.mutate();
            }}
          >
            {t('queue.confirm')}
          </button>
        </div>
      ) : (
        <form
          className="moderation-actions"
          onSubmit={(e) => {
            e.preventDefault();
            resolve.mutate();
          }}
        >
          <label htmlFor={`${id}-action`}>{t('queue.action')}</label>
          <select
            id={`${id}-action`}
            value={action}
            data-testid="queue-action"
            onChange={(e) => {
              setAction(e.target.value as Action);
            }}
          >
            {allowed.map((a) => (
              <option key={a} value={a}>
                {/* i18n-dynamic: moderation:action. */}
                {t(`action.${a}`)}
              </option>
            ))}
          </select>
          <label htmlFor={`${id}-reason`}>{t('queue.reason')}</label>
          <select
            id={`${id}-reason`}
            value={reason}
            data-testid="queue-reason"
            onChange={(e) => {
              setReason(e.target.value as ModReason);
            }}
          >
            {modReasons.map((r) => (
              <option key={r} value={r}>
                {t(`reason.${r}`)}
              </option>
            ))}
          </select>
          <button type="submit" disabled={resolve.isPending} data-testid="queue-resolve">
            {t('queue.resolve')}
          </button>
          {action === 'suspend_user' ? <small className="muted">{t('queue.twoPerson')}</small> : null}
        </form>
      )}
      {resolve.isSuccess && action === 'suspend_user' ? <p role="status">{t('queue.started')}</p> : null}
      {err ? (
        <p className="moderation-error" role="alert" data-testid="queue-error">
          {t(moderationErrorKey(err) as 'errors:unknown', errorParams(err))}
        </p>
      ) : null}
    </li>
  );
}
