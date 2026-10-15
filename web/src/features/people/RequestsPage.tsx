// Friend requests inbox (§4 rules: both sides must say yes). Owner: P0-W03 → P1-W03.
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Loading } from '../../app/Loading';
import { fetchRequests } from './api';
import { ErrorText, PeopleList, PeopleNav, peopleKey } from './shared';

export default function RequestsPage() {
  const { t } = useTranslation('people');
  const q = useQuery({ queryKey: [...peopleKey, 'requests'], queryFn: fetchRequests });
  return (
    <section className="page people-page">
      <h1>{t('requests.title')}</h1>
      <PeopleNav />
      {q.isPending ? (
        <Loading />
      ) : q.error ? (
        <ErrorText err={q.error} />
      ) : (
        <>
          <section className="people-section" aria-labelledby="requests-incoming">
            <h2 id="requests-incoming">{t('requests.incoming')}</h2>
            <PeopleList people={q.data.incoming} empty={t('requests.incomingEmpty')} />
          </section>
          <section className="people-section" aria-labelledby="requests-outgoing">
            <h2 id="requests-outgoing">{t('requests.outgoing')}</h2>
            <PeopleList people={q.data.outgoing} empty={t('requests.outgoingEmpty')} />
          </section>
        </>
      )}
    </section>
  );
}
