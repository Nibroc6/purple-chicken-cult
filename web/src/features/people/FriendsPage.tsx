// Placeholder page (SCAFFOLD hand-off stub). Owner: P0-W03 → P1-W03.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';

export default function FriendsPage() {
  const { t } = useTranslation('people');
  return <Placeholder title={t('friends.title')} />;
}
