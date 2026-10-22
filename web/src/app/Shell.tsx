// App shell (SCAFFOLD): the main navigation, the routed page, and feature slots.
// The navigation is a sidebar by default: collapsible to icons on wide screens
// (remembered per device), an off-canvas drawer on narrow ones. A user can pick
// a top header instead (display settings, <html data-nav="header">); the
// layouts are one markup restyled by CSS. The sidebar keeps the .shell-header
// and .nav-list classes so themes that style those still reach it. The
// secondary links (moreNav) sit at the sidebar's foot, or in a page footer when
// signed out or in the top-header layout on wide screens. On phones a bottom
// tab bar (tabNav) keeps the main pages under the thumb, and its last button
// opens everything else in the drawer.
import { Suspense, useCallback, useEffect, useId, useRef, useState, useSyncExternalStore } from 'react';
import { useTranslation } from 'react-i18next';
import { NavLink, Outlet, useLocation } from 'react-router';
import { Icon } from '../lib/Icon';
import { loadCollapsed, saveCollapsed } from '../lib/navLayout';
import { featureSlots } from './featureSlots';
import { Loading } from './Loading';
import { mainNav, moreNav, navAllows, tabNav, type MainNavEntry } from './mainNav';
import { usePageTitle } from './pageTitle';
import { useSession } from './session';

const navBadges = featureSlots.flatMap((s) => (s.navBadge ? [s.navBadge] : []));
const globals = featureSlots.flatMap((s) => s.global ?? []);

// The tab bar is only rendered on phones (the width below which the CSS turns
// the sidebar into a drawer), so wide screens carry no second copy of its
// links and badges. Without matchMedia (tests) it is always rendered.
const narrowQuery = '(max-width: 47.99rem)';
function subscribeNarrow(onChange: () => void): () => void {
  if (typeof window.matchMedia !== 'function') return () => undefined;
  const mq = window.matchMedia(narrowQuery);
  mq.addEventListener('change', onChange);
  return () => {
    mq.removeEventListener('change', onChange);
  };
}
function isNarrow(): boolean {
  return typeof window.matchMedia !== 'function' || window.matchMedia(narrowQuery).matches;
}

export function Shell() {
  const { t } = useTranslation('common');
  const session = useSession();
  const signedIn = session.data?.authenticated === true;
  const role = session.data?.me?.role;
  const visible = (list: MainNavEntry[]) => list.filter((e) => navAllows(e, signedIn, role));
  const navId = useId();
  const main = useRef<HTMLElement>(null);
  const format = useCallback((page: string) => t('app.pageTitle', { page }), [t]);
  usePageTitle(main, t('app.name'), format);
  const [collapsed, setCollapsed] = useState(loadCollapsed);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const menuButton = useRef<HTMLButtonElement>(null);
  const navRef = useRef<HTMLElement>(null);
  const location = useLocation();
  const narrow = useSyncExternalStore(subscribeNarrow, isNarrow);

  // Following a link closes the drawer.
  const [path, setPath] = useState(location.pathname);
  if (path !== location.pathname) {
    setPath(location.pathname);
    setDrawerOpen(false);
  }

  // An open drawer takes focus and closes on Escape, handing focus back.
  useEffect(() => {
    if (!drawerOpen) return;
    navRef.current?.querySelector<HTMLElement>('a')?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      setDrawerOpen(false);
      menuButton.current?.focus();
    };
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('keydown', onKey);
    };
  }, [drawerOpen]);

  const link = (e: MainNavEntry) => (
    <li key={e.to}>
      <NavLink to={e.to} end={e.to === '/'} title={collapsed ? t(e.label) : undefined}>
        <span className="nav-icon" aria-hidden="true">
          <Icon name={e.icon} />
        </span>
        <span className="nav-label">{t(e.label)}</span>
        {e.badges ? navBadges.map((Badge, i) => <Badge key={i} />) : null}
      </NavLink>
    </li>
  );

  const toggleCollapsed = () => {
    setCollapsed((c) => {
      saveCollapsed(!c);
      return !c;
    });
  };

  return (
    <div
      className="shell"
      data-signed-in={signedIn ? 'true' : 'false'}
      data-collapsed={collapsed ? 'true' : 'false'}
      data-drawer-open={drawerOpen ? 'true' : 'false'}
    >
      <a className="skip-link" href="#main">
        {t('nav.skip')}
      </a>
      <header className="shell-header sidebar">
        <div className="sidebar-head">
          <NavLink to="/" className="brand">
            {t('app.name')}
          </NavLink>
          {signedIn ? (
            <>
              <button
                type="button"
                className="sidebar-toggle"
                aria-expanded={!collapsed}
                aria-controls={navId}
                title={collapsed ? t('nav.expand') : t('nav.collapse')}
                onClick={toggleCollapsed}
              >
                <Icon name={collapsed ? 'expand' : 'collapse'} />
                <span className="visually-hidden">{collapsed ? t('nav.expand') : t('nav.collapse')}</span>
              </button>
            </>
          ) : null}
        </div>
        {signedIn ? (
          <nav id={navId} ref={navRef} className="sidebar-nav" aria-label={t('nav.label')}>
            <ul className="nav-list">{visible(mainNav).map(link)}</ul>
            <ul className="nav-list nav-more">{visible(moreNav).map(link)}</ul>
          </nav>
        ) : null}
      </header>
      {drawerOpen ? (
        <button
          type="button"
          className="sidebar-backdrop"
          tabIndex={-1}
          aria-label={t('nav.close')}
          onClick={() => {
            setDrawerOpen(false);
          }}
        />
      ) : null}
      <div className="shell-body" inert={drawerOpen}>
        <main id="main" className="shell-main" ref={main}>
          <Suspense fallback={<Loading />}>
            <Outlet />
          </Suspense>
        </main>
        <footer className="site-footer" data-testid="site-footer">
          <nav aria-label={t('nav.more')}>
            <ul className="link-list">
              {visible(moreNav).map((e) => (
                <li key={e.to}>
                  <NavLink to={e.to}>{t(e.label)}</NavLink>
                </li>
              ))}
            </ul>
          </nav>
        </footer>
        {globals.map((G, i) => (
          <G key={i} />
        ))}
      </div>
      {signedIn && narrow ? (
        <nav className="tabbar" aria-label={t('nav.tabs')}>
          <ul>
            {visible(tabNav).map((e) => (
              <li key={e.to} data-primary={e.to === '/compose' ? 'true' : undefined}>
                <NavLink to={e.to} end={e.to === '/'}>
                  <span className="tabbar-icon" aria-hidden="true">
                    <Icon name={e.icon} />
                  </span>
                  <span className="tabbar-label">{t(e.short ?? e.label)}</span>
                  {e.badges ? navBadges.map((Badge, i) => <Badge key={i} />) : null}
                </NavLink>
              </li>
            ))}
            <li>
              <button
                ref={menuButton}
                type="button"
                className="sidebar-menu"
                aria-expanded={drawerOpen}
                aria-controls={navId}
                onClick={() => {
                  setDrawerOpen((o) => !o);
                }}
              >
                <span className="tabbar-icon" aria-hidden="true">
                  <Icon name={drawerOpen ? 'close' : 'menu'} />
                </span>
                <span className="tabbar-label" aria-hidden="true">
                  {t('nav.menu')}
                </span>
                <span className="visually-hidden">{drawerOpen ? t('nav.close') : t('nav.menu')}</span>
              </button>
            </li>
          </ul>
        </nav>
      ) : null}
    </div>
  );
}
