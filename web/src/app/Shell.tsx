// App shell (SCAFFOLD): header navigation, the routed page, and feature slots.
import { Suspense } from 'react';
import { useTranslation } from 'react-i18next';
import { NavLink, Outlet } from 'react-router';
import { featureSlots } from './featureSlots';
import { Loading } from './Loading';
import { useSession } from './session';

const navBadges = featureSlots.flatMap((s) => (s.navBadge ? [s.navBadge] : []));
const globals = featureSlots.flatMap((s) => s.global ?? []);

export function Shell() {
  const { t } = useTranslation('common');
  const session = useSession();
  const signedIn = session.data?.authenticated === true;
  return (
    <div className="shell">
      <a className="skip-link" href="#main">
        {t('nav.label')}
      </a>
      <header className="shell-header">
        <NavLink to="/" className="brand">
          {t('app.name')}
        </NavLink>
        {signedIn ? (
          <nav aria-label={t('nav.label')}>
            <ul className="nav-list">
              <li>
                <NavLink to="/">{t('nav.feed')}</NavLink>
              </li>
              <li>
                <NavLink to="/compose">{t('nav.compose')}</NavLink>
              </li>
              <li>
                <NavLink to="/invite">{t('nav.invite')}</NavLink>
              </li>
              <li>
                <NavLink to="/friends">{t('nav.friends')}</NavLink>
              </li>
              <li>
                <NavLink to="/notifications">
                  {t('nav.notifications')}
                  {navBadges.map((Badge, i) => (
                    <Badge key={i} />
                  ))}
                </NavLink>
              </li>
              <li>
                <NavLink to="/settings">{t('nav.settings')}</NavLink>
              </li>
            </ul>
          </nav>
        ) : null}
      </header>
      <main id="main" className="shell-main">
        <Suspense fallback={<Loading />}>
          <Outlet />
        </Suspense>
      </main>
      {globals.map((G, i) => (
        <G key={i} />
      ))}
    </div>
  );
}
