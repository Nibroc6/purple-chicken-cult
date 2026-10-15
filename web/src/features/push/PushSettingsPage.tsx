// Placeholder page (SCAFFOLD hand-off stub). Owner: P1-W05.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';

export default function PushSettingsPage() {
  const { t } = useTranslation('push');
  return <Placeholder title={t('page.title')} />;
}
