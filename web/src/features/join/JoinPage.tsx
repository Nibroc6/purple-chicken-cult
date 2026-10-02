// Placeholder page (SCAFFOLD hand-off stub). Owner: P0-W04 → P1-W02.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';

export default function JoinPage() {
  const { t } = useTranslation('join');
  return <Placeholder title={t('page.title')} />;
}
