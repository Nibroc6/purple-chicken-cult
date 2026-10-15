// Placeholder page (SCAFFOLD hand-off stub). Owner: P1-W08.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';

export default function SavesPage() {
  const { t } = useTranslation('saves');
  return <Placeholder title={t('page.title')} />;
}
