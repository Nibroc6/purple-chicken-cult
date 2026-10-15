// Placeholder page (SCAFFOLD hand-off stub). Owner: P0-W04 → P1-W02.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';

export default function InviteesPage() {
  const { t } = useTranslation('invites');
  return <Placeholder title={t('people.title')} />;
}
