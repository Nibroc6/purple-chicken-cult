// Blocked people (settings). A block is never shown to the blocked person.
// Owner: P0-W03 → P1-W03.
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Loading } from '../../app/Loading';
import { fetchBlocks } from './api';
import { ErrorText, PeopleList, peopleKey } from './shared';
import { InfoTip } from '../../lib/InfoTip';
import { SettingsSubNav } from '../../app/SettingsSubNav';

export default function BlocksPage() {
  const { t } = useTranslation(['people', 'common']);
  const q = useQuery({ queryKey: [...peopleKey, 'blocks'], queryFn: fetchBlocks });
  return (
    <section className="page people-page">
      <h1>{t('blocks.title')}</h1>
      <SettingsSubNav />
      <p className="muted">
        {t('blocks.helpShort')}
        <InfoTip>{t('blocks.help')}</InfoTip>
      </p>
      {q.isPending ? <Loading /> : q.error ? <ErrorText err={q.error} /> : <PeopleList people={q.data} empty={t('blocks.empty')} />}
    </section>
  );
}
