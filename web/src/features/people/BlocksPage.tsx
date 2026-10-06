// Blocked people (settings). A block is never shown to the blocked person.
// Owner: P0-W03 → P1-W03.
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { Loading } from '../../app/Loading';
import { fetchBlocks } from './api';
import { ErrorText, PeopleList, peopleKey } from './shared';

export default function BlocksPage() {
  const { t } = useTranslation(['people', 'common']);
  const q = useQuery({ queryKey: [...peopleKey, 'blocks'], queryFn: fetchBlocks });
  return (
    <section className="page people-page">
      <h1>{t('blocks.title')}</h1>
      <p>
        <Link to="/settings">{t('common:nav.settings')}</Link>
      </p>
      <p className="muted">{t('blocks.help')}</p>
      {q.isPending ? <Loading /> : q.error ? <ErrorText err={q.error} /> : <PeopleList people={q.data} empty={t('blocks.empty')} />}
    </section>
  );
}
