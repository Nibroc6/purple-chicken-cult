// The settings sections as tabs (SCAFFOLD); every settings page renders it
// under its title.
import { useTranslation } from 'react-i18next';
import { visibleSettingsNav } from './settingsNav';
import { SubNav } from './SubNav';

export function SettingsSubNav() {
  const { t } = useTranslation('common');
  return <SubNav label={t('settingsNav.label')} items={visibleSettingsNav.map((e) => ({ to: e.path, label: t(e.label) }))} />;
}
