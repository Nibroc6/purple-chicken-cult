// Settings index (SCAFFOLD hand-off stub). Owner: P0-W11 → P1-W10.
import { useTranslation } from 'react-i18next';
import { Placeholder } from '../../app/Placeholder';
import { SettingsIndexNav } from '../../app/SettingsIndexNav';

export default function SettingsPage() {
  const { t } = useTranslation('settings');
  return (
    <>
      <Placeholder title={t('index.title')} />
      <SettingsIndexNav />
    </>
  );
}
