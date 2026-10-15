// Placeholder page (SCAFFOLD hand-off stub). Owner: P0-W12.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';

export default function LandingPage() {
  const { t } = useTranslation('landing');
  return <Placeholder title={t('page.title')} />;
}
