// Placeholder page (SCAFFOLD hand-off stub). Owner: P0-W11 → P1-W10.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';

export default function ModRecoveryPage() {
  const { t } = useTranslation('modrecovery');
  return <Placeholder title={t('page.title')} />;
}
