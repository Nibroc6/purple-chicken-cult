// Placeholder page (SCAFFOLD hand-off stub). Owner: P0-W11 → P1-W10.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';

export default function ExportPage() {
  const { t } = useTranslation('settings');
  return <Placeholder title={t('export.title')} />;
}
