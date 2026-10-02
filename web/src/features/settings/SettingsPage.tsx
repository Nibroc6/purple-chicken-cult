// Settings index: links to every settings page (app/settingsNav.ts).
// Owner: P0-W11 → P1-W10.
import { useTranslation } from 'react-i18next';
import { SettingsIndexNav } from '../../app/SettingsIndexNav';

export default function SettingsPage() {
  const { t } = useTranslation('settings');
  return (
    <section className="page">
      <h1>{t('index.title')}</h1>
      <SettingsIndexNav />
    </section>
  );
}
