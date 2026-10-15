// Placeholder page (SCAFFOLD hand-off stub). Owner: P1-W09.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';

export default function ThemePage() {
  const { t } = useTranslation('themes');
  return <Placeholder title={t('page.title')} />;
}
