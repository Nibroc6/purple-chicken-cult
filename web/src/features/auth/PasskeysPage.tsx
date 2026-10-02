// Placeholder page (SCAFFOLD hand-off stub). Owner: P0-W01 → P1-W07.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';

export default function PasskeysPage() {
  const { t } = useTranslation('auth');
  return <Placeholder title={t('passkeys.title')} />;
}
