// Placeholder page (SCAFFOLD hand-off stub). Owner: P0-W07 → P1-W08.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';

export default function FeedPage() {
  const { t } = useTranslation('feed');
  return <Placeholder title={t('page.title')} />;
}
