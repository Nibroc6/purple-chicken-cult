// A person's page: who they are to you, how you are connected, who invited
// them, and the friendship actions. Owner: P0-W03 → P1-W03.
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useParams } from 'react-router';
import { Loading } from '../../app/Loading';
import { NotFound } from '../../app/NotFound';
import { ApiError } from '../../api/errors';
import { asRef } from '../../vm/types';
import { fetchPerson } from './api';
import { ErrorText, PeopleNav, PersonActions, PersonCard, ReportPerson, peopleKey } from './shared';

// i18n-dynamic: people:person.relation.
export default function PersonPage() {
  const { t } = useTranslation('people');
  const ref = asRef(useParams().ref ?? '');
  const q = useQuery({ queryKey: [...peopleKey, 'person', ref], queryFn: () => fetchPerson(ref), retry: false });
  if (q.error instanceof ApiError && q.error.isNotFound) return <NotFound />;
  return (
    <section className="page people-page">
      <h1>{t('person.title')}</h1>
      <PeopleNav />
      {q.isPending ? (
        <Loading />
      ) : q.error ? (
        <ErrorText err={q.error} />
      ) : (
        <ul className="people-list">
          <PersonCard
            person={q.data}
            extra={
              <span className="muted" data-testid="person-relation">
                {t(`person.relation.${q.data.blocked ? 'blocked' : q.data.request ? q.data.request : q.data.relation}` as 'person.relation.self')}
              </span>
            }
          >
            <PersonActions person={q.data} secondary />
            {q.data.relation !== 'self' ? <ReportPerson person={q.data} /> : null}
          </PersonCard>
        </ul>
      )}
    </section>
  );
}
