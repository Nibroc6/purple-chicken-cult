// Placeholder page (SCAFFOLD hand-off stub). Owner: P0-W05 → P1-W12.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';

export default function ComposePage() {
  const { t } = useTranslation('composer');
  return <Placeholder title={t('page.title')} />;
}
