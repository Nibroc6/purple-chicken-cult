// Moderator queue (/mod; §8, §9(5), §10.4). Owner: P0-W12 → P1-W06. One queue
// for reports, CSAM holds, behavioral signals, step-up repeats and invite
// clusters, most urgent first. Moderators see the reported content (never for
// csam: a CSAM hold shows only the scanner's match metadata and its
// preservation state), the subject's handle, inviter label and probation flag,
// and nothing else of the graph. remove_post and dismiss take one moderator;
// suspend_user, the csam report, dismissing a CSAM hold and the invite-tree
// quarantine need a fresh passkey confirmation and then a second, different
// moderator within 24 hours, who co-signs the exact ModAction record with
// their own passkey. A 401 step_up_required opens the global passkey step-up;
// the moderator then retries.
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { errorKey } from '../../api/errors';
import { Loading } from '../../app/Loading';
import { useMe } from '../../app/session';
import { formatDateTime, formatRelative } from '../../lib/time';
import { requestCosign } from '../cosign';
import {
  actionsFor,
  canQuarantine,
  claimItem,
  confirmItem,
  errorParams,
  fetchQueue,
  modReasons,
  moderationErrorKey,
  reportRefOK,
  resolveItem,
  sessionCosigns,
  startCSAMReport,
  startQuarantine,
  type Action,
  type CSAMHold,
  type Cosig,
  type ModItem,
  type ModReason,
} from './api';
import './moderation.css';
import { InfoTip } from '../../lib/InfoTip';

export const queueKey = ['moderation', 'queue'] as const;
const cosignsKey = ['moderation', 'cosigns'] as const;

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
  const me = useMe();
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
      <p className="muted">
        {t('queue.intro')}
        <InfoTip>{t('queue.introTip')}</InfoTip>
      </p>
      <ul className="link-list link-cards">
        <li>
          <Link to="/mod/log">{t('footer.log')}</Link>
        </li>
        <li>
          <Link to="/mod/recovery">{t('footer.recovery')}</Link>
        </li>
        {me?.role === 'admin' ? (
          <li>
            <Link to="/mod/roles" data-testid="queue-roles-link">
              {t('footer.roles')}
            </Link>
          </li>
        ) : null}
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

function initialReason(r: string): ModReason {
  return (modReasons as readonly string[]).includes(r) ? (r as ModReason) : r === 'csam' ? 'csam' : 'bot';
}

// The summary counts shown as facts (signals, invite clusters, quarantine).
const facts = ['posts', 'hours', 'accounts', 'shared_network', 'inviter_marks'] as const;

// i18n-dynamic: moderation:source.
// i18n-dynamic: moderation:signal.
// i18n-dynamic: moderation:fact.
function Heading({ it }: { it: ModItem }) {
  const { t } = useTranslation('moderation');
  const reports = it.summary?.reports ?? 1;
  const label =
    it.source === 'report' || it.source === 'csam'
      ? t(`reason.${it.reason}` as 'reason.other')
      : t(`signal.${it.reason}` as 'signal.signal');
  return (
    <p className="moderation-text">
      <strong>{label}</strong>
      {it.priority === 0 ? <span className="moderation-tag">{t('queue.urgent')}</span> : null}{' '}
      <span className="muted" data-testid="queue-source">
        {t(`source.${it.source}` as 'source.report')}
      </span>{' '}
      {it.source === 'report' ? <span className="muted">{t('queue.reports', { count: reports })}</span> : null}
    </p>
  );
}

function Facts({ it }: { it: ModItem }) {
  const { t } = useTranslation('moderation');
  const s = it.summary ?? {};
  const shown = facts.filter((f) => (s[f] ?? 0) > 0);
  const subtree = s.subtree_confirmed !== undefined && s.quarantine_k !== undefined && s.subtree_confirmed > 0;
  if (shown.length === 0 && !subtree) return null;
  return (
    <ul className="moderation-facts" data-testid="queue-facts">
      {shown.map((f) => (
        <li key={f} data-fact={f}>
          {t(`fact.${f}` as 'fact.posts', { count: s[f] ?? 0 })}
        </li>
      ))}
      {subtree ? (
        <li data-fact="subtree_confirmed" data-testid="queue-subtree">
          {t('fact.subtree_confirmed', { count: s.subtree_confirmed ?? 0, k: s.quarantine_k ?? 0 })}
        </li>
      ) : null}
    </ul>
  );
}

// CSAMPanel shows a hold's match metadata and preservation state — never
// content, never a link (the source URL is text).
function CSAMPanel({ hold }: { hold: CSAMHold }) {
  const { t, i18n } = useTranslation('moderation');
  const entries = Object.entries(hold.match_info).sort(([a], [b]) => a.localeCompare(b));
  const date = (s: string) => formatDateTime(new Date(s), i18n.language);
  return (
    <div className="moderation-csam" data-testid="queue-csam">
      <p className="muted">{t('csam.noContent')}</p>
      <dl>
        {entries.map(([k, v]) => (
          <div key={k} className="moderation-row">
            <dt>
              <code className="mono">{k}</code>
            </dt>
            <dd data-user-content="">{v}</dd>
          </div>
        ))}
        {hold.source_url ? (
          <div className="moderation-row">
            <dt>{t('csam.sourceURL')}</dt>
            <dd>
              <code className="mono" data-testid="queue-csam-url">
                {hold.source_url}
              </code>
            </dd>
          </div>
        ) : null}
        <div className="moderation-row">
          <dt>{t('csam.heldAt')}</dt>
          <dd>{date(hold.held_at)}</dd>
        </div>
        <div className="moderation-row">
          <dt>{t('csam.retainUntil')}</dt>
          <dd>{date(hold.retain_until)}</dd>
        </div>
        <div className="moderation-row">
          <dt>{t('csam.reported')}</dt>
          <dd data-testid="queue-csam-reported">
            {hold.reported_at ? t('csam.reportedAt', { date: date(hold.reported_at), ref: hold.report_ref }) : t('csam.notReported')}
          </dd>
        </div>
        <div className="moderation-row">
          <dt>{t('csam.account')}</dt>
          <dd>{hold.account_held ? t('csam.accountHeld') : t('csam.accountReleased')}</dd>
        </div>
      </dl>
    </div>
  );
}

// i18n-dynamic: moderation:awaiting.
// i18n-dynamic: moderation:cosign.
function QueueItem({ it }: { it: ModItem }) {
  const { t, i18n } = useTranslation(['moderation', 'errors']);
  const qc = useQueryClient();
  const id = useId();
  const allowed = actionsFor(it);
  const [action, setAction] = useState<Action>(allowed[0] ?? 'dismiss');
  const [reason, setReason] = useState<ModReason>(initialReason(it.reason));
  const [reportRef, setReportRef] = useState('');
  const cosigns = useQuery({ queryKey: cosignsKey, queryFn: sessionCosigns, staleTime: Infinity });
  const done = () => qc.invalidateQueries({ queryKey: queueKey });
  const resolve = useMutation({ mutationFn: () => resolveItem(it.id, action, reason), onSuccess: done });
  const claim = useMutation({ mutationFn: () => claimItem(it.id), onSuccess: done });
  const csam = useMutation({ mutationFn: () => startCSAMReport(it.id, reportRef.trim()), onSuccess: done });
  const quarantine = useMutation({ mutationFn: () => startQuarantine(it.id), onSuccess: done });
  const confirm = useMutation({
    // A passkey session co-signs the ModAction record first; a cancelled
    // dialog changes nothing.
    mutationFn: async () => {
      let cosig: Cosig | undefined;
      if (cosigns.data ?? (await sessionCosigns())) {
        const action = it.pending?.action ?? 'suspend_user';
        const r = await requestCosign({
          purpose: 'mod_action',
          target: it.id,
          summary: t(`cosign.${action}` as 'cosign.suspend_user', { handle: it.subject?.handle ?? '' }),
        });
        if (!r) return false;
        cosig = r;
      }
      await confirmItem(it.id, cosig);
      return true;
    },
    onSuccess: async (ok) => {
      if (ok) await done();
    },
  });
  const err = resolve.error ?? confirm.error ?? claim.error ?? csam.error ?? quarantine.error;
  const at = new Date(it.at);
  const twoPerson = action === 'suspend_user' || (action === 'dismiss' && it.source === 'csam');
  return (
    <li
      className="moderation-item"
      data-testid="queue-item"
      data-reason={it.reason}
      data-source={it.source}
      data-awaiting={it.awaiting_second}
      data-claimed={it.claimed === true}
    >
      <Heading it={it} />
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
      {it.subject ? (
        // The moderator-scoped ref the recovery page asks for (§10.3).
        <p className="moderation-meta muted">
          {t('queue.subjectRef')}{' '}
          <code className="mono" data-testid="queue-subject-ref">
            {it.subject.ref}
          </code>
        </p>
      ) : null}
      <p className="moderation-meta muted">
        <time dateTime={at.toISOString()} title={formatDateTime(at, i18n.language)}>
          {formatRelative(at, i18n.language)}
        </time>
      </p>
      <Facts it={it} />
      {it.post ? (
        <blockquote className="moderation-post" data-user-content="" data-testid="queue-post">
          {it.post.body}
        </blockquote>
      ) : it.reason === 'csam' && !it.csam ? (
        <p className="muted">{t('queue.noContent')}</p>
      ) : null}
      {it.csam ? <CSAMPanel hold={it.csam} /> : null}
      <div className="moderation-claim">
        {it.claimed_by_me ? (
          <span className="moderation-tag" data-testid="queue-claimed">
            {t('queue.claimedByMe')}
          </span>
        ) : it.claimed ? (
          <span className="moderation-tag" data-testid="queue-claimed">
            {t('queue.claimedByOther')}
          </span>
        ) : null}
        {!it.claimed_by_me && !it.awaiting_second ? (
          <button
            type="button"
            disabled={claim.isPending}
            data-testid="queue-claim"
            onClick={() => {
              claim.mutate();
            }}
          >
            {t('queue.claim')}
          </button>
        ) : null}
      </div>
      {it.awaiting_second ? (
        <div className="moderation-actions">
          <p role="status" data-testid="queue-awaiting" data-action={it.pending?.action ?? 'suspend_user'}>
            {t(`awaiting.${it.pending?.action ?? 'suspend_user'}` as 'awaiting.suspend_user', {
              ref: it.pending?.report_ref ?? '',
            })}
            {it.pending?.mine ? <> {t('queue.awaitingMine')}</> : null}
          </p>
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
        <>
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
            {twoPerson ? <small className="muted">{t('queue.twoPerson')}</small> : null}
          </form>
          {it.source === 'csam' && !it.csam?.reported_at ? (
            <form
              className="moderation-actions"
              data-testid="queue-csam-form"
              onSubmit={(e) => {
                e.preventDefault();
                if (reportRefOK(reportRef.trim())) csam.mutate();
              }}
            >
              <label htmlFor={`${id}-ref`}>{t('csam.reportRef')}</label>
              <input
                id={`${id}-ref`}
                className="mono"
                value={reportRef}
                maxLength={128}
                autoComplete="off"
                spellCheck={false}
                aria-describedby={`${id}-ref-hint`}
                data-testid="queue-csam-ref"
                onChange={(e) => {
                  setReportRef(e.target.value);
                }}
              />
              <button type="submit" disabled={csam.isPending || !reportRefOK(reportRef.trim())} data-testid="queue-csam-report">
                {t('csam.start')}
              </button>
              <small id={`${id}-ref-hint`} className="muted">
                {t('csam.hint')}
              </small>
            </form>
          ) : null}
          {canQuarantine(it) ? (
            <div className="moderation-actions">
              <button
                type="button"
                disabled={quarantine.isPending}
                data-testid="queue-quarantine"
                onClick={() => {
                  quarantine.mutate();
                }}
              >
                {t('queue.quarantine', { handle: it.subject_inviter?.handle ?? '' })}
              </button>
              <small className="muted">{t('queue.quarantineHint')}</small>
            </div>
          ) : null}
        </>
      )}
      {resolve.isSuccess && twoPerson ? <p role="status">{t('queue.started')}</p> : null}
      {csam.isSuccess || quarantine.isSuccess ? <p role="status">{t('queue.started')}</p> : null}
      {err ? (
        <p className="moderation-error" role="alert" data-testid="queue-error">
          {t(moderationErrorKey(err) as 'errors:unknown', errorParams(err))}
        </p>
      ) : null}
    </li>
  );
}
