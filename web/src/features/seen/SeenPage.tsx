// Placeholder page (SCAFFOLD hand-off stub). Owner: P0-W06 → P1-W08.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';

export default function SeenPage() {
  const { t } = useTranslation('seen');
  return <Placeholder title={t('page.title')} />;
}
