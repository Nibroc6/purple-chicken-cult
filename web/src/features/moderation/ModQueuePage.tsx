// Placeholder page (SCAFFOLD hand-off stub). Owner: P0-W12 → P1-W06.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';

export default function ModQueuePage() {
  const { t } = useTranslation('moderation');
  return <Placeholder title={t('queue.title')} />;
}
