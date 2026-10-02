// Placeholder page (SCAFFOLD hand-off stub). Owner: P0-W08 → P1-W09.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';

export default function TreePage() {
  const { t } = useTranslation('tree');
  return <Placeholder title={t('page.title')} />;
}
