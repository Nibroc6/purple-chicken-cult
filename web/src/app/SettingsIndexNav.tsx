// The settings index navigation (SCAFFOLD); rendered by features/settings.
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { visibleSettingsNav } from './settingsNav';

export function SettingsIndexNav() {
  const { t } = useTranslation('common');
  return (
    <nav aria-label={t('settingsNav.label')}>
      <ul className="link-list">
        {visibleSettingsNav.map((e) => (
          <li key={e.path}>
            <Link to={e.path}>{t(e.label)}</Link>
          </li>
        ))}
      </ul>
    </nav>
  );
}
