// Placeholder page (SCAFFOLD hand-off stub). Owner: P0-W03 → P1-W03.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';

export default function PersonPage() {
  const { t } = useTranslation('people');
  return <Placeholder title={t('person.title')} />;
}
