// The main navigation (SCAFFOLD-owned list): every entry of the sidebar, or of
// the top header when a user picks that layout. A feature adds its page here.
// Labels live in the common namespace. i18n-dynamic: common:nav.
export interface MainNavEntry {
  to: string;
  label: 'nav.feed' | 'nav.compose' | 'nav.invite' | 'nav.people' | 'nav.notifications' | 'nav.settings';
  // A short glyph shown when the sidebar is collapsed (decorative).
  icon: string;
  // The feature nav badges render next to this entry (e.g. the unread count).
  badges?: boolean;
}

export const mainNav: MainNavEntry[] = [
  { to: '/', label: 'nav.feed', icon: '⌂' },
  { to: '/compose', label: 'nav.compose', icon: '✎' },
  { to: '/invite', label: 'nav.invite', icon: '✉' },
  { to: '/friends', label: 'nav.people', icon: '☺' },
  { to: '/notifications', label: 'nav.notifications', icon: '♪', badges: true },
  { to: '/settings', label: 'nav.settings', icon: '⚙' },
];
