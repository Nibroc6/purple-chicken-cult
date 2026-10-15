// The inviter's own invitee list (§8): who joined through their invites and,
// visible only to the inviter, who is still on probation. Owner: P0-W04 → P1-W02.
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { Loading } from '../../app/Loading';
import { formatDateTime } from '../../lib/time';
import { PersonCard } from '../people/shared';
import { fetchInvitees } from './api';
import { ErrorText, invitesKey } from './shared';
import type { InviteeVM } from './vm';

export default function InviteesPage() {
  const { t } = useTranslation('invites');
  const q = useQuery({ queryKey: [...invitesKey, 'invitees'], queryFn: fetchInvitees });
  return (
    <section className="page invites-page">
      <h1>{t('people.title')}</h1>
      <p>
        <Link to="/invite">{t('people.back')}</Link>
      </p>
      {q.isPending ? <Loading /> : q.error ? <ErrorText err={q.error} /> : <List items={q.data} />}
    </section>
  );
}

function List({ items }: { items: InviteeVM[] }) {
  const { t, i18n } = useTranslation('invites');
  if (items.length === 0) return <p className="muted">{t('people.empty')}</p>;
  return (
    <ul className="people-list">
      {items.map((v) => (
        <PersonCard
          key={v.person.ref}
          person={v.person}
          extra={
            <>
              <p className="muted invites-meta">{t('people.joined', { date: formatDateTime(v.joinedAt, i18n.language) })}</p>
              {v.probationUntil ? (
                <p className="invites-meta" data-testid="invitee-probation">
                  {t('people.probation', { date: formatDateTime(v.probationUntil, i18n.language) })}
                </p>
              ) : null}
            </>
          }
        />
      ))}
    </ul>
  );
}
