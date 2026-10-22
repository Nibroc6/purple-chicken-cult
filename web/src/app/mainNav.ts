// The main navigation (SCAFFOLD-owned list): every entry of the sidebar, or of
// the top header when a user picks that layout. A feature adds its page here.
// Labels live in the common namespace. i18n-dynamic: common:nav.
export interface MainNavEntry {
  to: string;
  label: 'nav.feed' | 'nav.compose' | 'nav.invite' | 'nav.people' | 'nav.saves' | 'nav.seen' | 'nav.notifications' | 'nav.moderation' | 'nav.settings' | 'nav.about';
  // A short glyph shown when the sidebar is collapsed (decorative).
  icon: string;
  // An outline icon (24×24 stroke paths) drawn instead of the glyph.
  paths?: string[];
  // The feature nav badges render next to this entry (e.g. the unread count).
  badges?: boolean;
  // Shown to moderators and admins only.
  moderator?: boolean;
}

export const mainNav: MainNavEntry[] = [
  { to: '/', label: 'nav.feed', icon: '⌂' },
  { to: '/compose', label: 'nav.compose', icon: '✎' },
  // Lucide "feather" (ISC licence).
  {
    to: '/invite',
    label: 'nav.invite',
    icon: '✉',
    paths: [
      'M12.67 19a2 2 0 0 0 1.416-.588l6.154-6.172a6 6 0 0 0-8.49-8.49L5.586 9.914A2 2 0 0 0 5 11.328V18a1 1 0 0 0 1 1z',
      'M16 8 2 22',
      'M17.5 15H9',
    ],
  },
  { to: '/friends', label: 'nav.people', icon: '☺' },
  { to: '/saves', label: 'nav.saves', icon: '★' },
  { to: '/seen', label: 'nav.seen', icon: '◉' },
  { to: '/notifications', label: 'nav.notifications', icon: '♪', badges: true },
  { to: '/mod', label: 'nav.moderation', icon: '⚑', moderator: true },
  { to: '/settings', label: 'nav.settings', icon: '⚙' },
  { to: '/about', label: 'nav.about', icon: '?' },
];
