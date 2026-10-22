// Public moderation log (/mod/log; §10.4). Owner: P0-W12 → P1-W06. Confirmed
// removals and suspensions per day with their reason codes and counts. A person
// is named (and linked) only if the reader already has them in their horizon;
// everyone else appears under a pseudonym.
import { useInfiniteQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { errorText } from '../../api/errors';
import { Loading } from '../../app/Loading';
import { fetchLog, type LogItem } from './api';
import './moderation.css';
import { InfoTip } from '../../lib/InfoTip';

export const logKey = ['moderation', 'log'] as const;

export default function ModLogPage() {
  const { t } = useTranslation(['moderation', 'errors']);
  const q = useInfiniteQuery({
    queryKey: logKey,
    queryFn: ({ pageParam }) => fetchLog(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next,
    retry: false,
  });
  const items = q.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <section className="page moderation-page" data-testid="mod-log-page">
      <h1>{t('log.title')}</h1>
      <p className="muted">
        {t('log.intro')}
        <InfoTip>{t('log.introTip')}</InfoTip>
      </p>
      {q.isPending ? <Loading /> : null}
      {q.error ? (
        <p className="moderation-error" role="alert">
          {errorText(t, q.error)}
        </p>
      ) : null}
      {q.isSuccess && items.length === 0 ? (
        <p className="muted" data-testid="log-empty">
          {t('log.empty')}
        </p>
      ) : null}
      <table className="moderation-table" data-testid="log-table">
        <thead>
          <tr>
            <th scope="col">{t('log.day')}</th>
            <th scope="col">{t('log.action')}</th>
            <th scope="col">{t('log.reason')}</th>
            <th scope="col">{t('log.subject')}</th>
            <th scope="col">{t('log.count')}</th>
          </tr>
        </thead>
        <tbody>
          {items.map((it, i) => (
            <LogRow key={`${it.day}-${String(i)}`} it={it} />
          ))}
        </tbody>
      </table>
      {q.hasNextPage ? (
        <button
          type="button"
          disabled={q.isFetchingNextPage}
          onClick={() => {
            void q.fetchNextPage();
          }}
        >
          {t('log.more')}
        </button>
      ) : null}
    </section>
  );
}

function LogRow({ it }: { it: LogItem }) {
  const { t } = useTranslation('moderation');
  const s = it.subject;
  return (
    <tr data-testid="log-row" data-action={it.action}>
      <td>{it.day}</td>
      {/* i18n-dynamic: moderation:action. */}
      <td>{t(`action.${it.action}` as 'action.dismiss')}</td>
      {/* i18n-dynamic: moderation:reason. */}
      <td>{t(`reason.${it.reason}` as 'reason.other')}</td>
      <td data-testid="log-subject">
        {s?.ref && s.handle ? (
          <Link to={`/people/${s.ref}`} data-testid="log-person">
            {t('log.handle', { handle: s.handle })}
          </Link>
        ) : s?.pseudonym ? (
          <span className="mono" data-testid="log-pseudonym" title={t('log.pseudonymHint')}>
            {t('log.pseudonym', { id: s.pseudonym })}
          </span>
        ) : null}
      </td>
      <td>{it.count}</td>
    </tr>
  );
}
