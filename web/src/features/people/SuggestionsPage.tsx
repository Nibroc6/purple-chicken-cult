// People you may know (§10.5): mutual friend count only. Owner: P0-W03 → P1-W03.
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Loading } from '../../app/Loading';
import { fetchSuggestions } from './api';
import { ErrorText, PeopleNav, PersonCard, peopleKey } from './shared';

export default function SuggestionsPage() {
  const { t } = useTranslation('people');
  const q = useQuery({ queryKey: [...peopleKey, 'suggestions'], queryFn: fetchSuggestions });
  return (
    <section className="page people-page">
      <h1>{t('suggestions.title')}</h1>
      <PeopleNav />
      {q.isPending ? (
        <Loading />
      ) : q.error ? (
        <ErrorText err={q.error} />
      ) : q.data.length === 0 ? (
        <p className="muted">{t('suggestions.empty')}</p>
      ) : (
        <ul className="people-list">
          {q.data.map((s) => (
            <PersonCard
              key={s.person.ref}
              person={s.person}
              extra={<span className="muted">{t('suggestions.mutual', { count: s.mutual })}</span>}
            />
          ))}
        </ul>
      )}
    </section>
  );
}
