// Placeholder page (SCAFFOLD hand-off stub). Owner: P0-W10 → P1-W05.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';

export default function NotificationsPage() {
  const { t } = useTranslation('notifications');
  return <Placeholder title={t('page.title')} />;
}
