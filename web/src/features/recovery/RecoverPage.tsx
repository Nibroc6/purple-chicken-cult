// Placeholder page (SCAFFOLD hand-off stub). Owner: P1-W10.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';

export default function RecoverPage() {
  const { t } = useTranslation('recovery');
  return <Placeholder title={t('recover.title')} />;
}
