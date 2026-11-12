// The settings index navigation (SCAFFOLD); rendered by features/settings.
// Sections are grouped (appearance, privacy and safety, account, then delete
// on its own), each entry with a one-line description.
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { settingsGroups, visibleSettingsNav } from './settingsNav';

// i18n-dynamic: common:settingsHint.
export function SettingsIndexNav() {
  const { t } = useTranslation('common');
  return (
    <nav aria-label={t('settingsNav.label')} className="settings-index">
      {settingsGroups.map((g) => {
        const entries = visibleSettingsNav.filter((e) => e.group === g);
        if (entries.length === 0) return null;
        return (
          <section key={g} className="settings-group" data-group={g}>
            <h2>{t(`settingsGroups.${g}`)}</h2>
            <ul className="link-list link-cards">
              {entries.map((e) => (
                <li key={e.path}>
                  <Link to={e.path}>
                    <span className="settings-entry-title">{t(e.label)}</span>
                    <span className="settings-entry-hint">{t(`settingsHint.${e.label.slice('settingsNav.'.length)}` as 'settingsHint.display')}</span>
                  </Link>
                </li>
              ))}
            </ul>
          </section>
        );
      })}
    </nav>
  );
}
