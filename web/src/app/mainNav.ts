// The main navigation (SCAFFOLD-owned list): every entry of the sidebar, or of
// the top header when a user picks that layout. A feature adds its page here.
// moreNav is the smaller list under it (about, terms and moderation pages, the old
// site footer); signed out, or in the top-header layout on wide screens, it
// renders as a page footer instead. Labels live in the common namespace.
// i18n-dynamic: common:nav.
import type { IconName } from '../lib/Icon';

export type NavAudience = 'everyone' | 'member' | 'moderator' | 'admin';

export interface MainNavEntry {
  to: string;
  label:
    | 'nav.feed'
    | 'nav.compose'
    | 'nav.invite'
    | 'nav.people'
    | 'nav.saves'
    | 'nav.seen'
    | 'nav.notifications'
    | 'nav.messages'
    | 'nav.moderation'
    | 'nav.settings'
    | 'nav.about'
    | 'nav.terms'
    | 'nav.modLog'
    | 'nav.blame'
    | 'nav.modRecovery'
    | 'nav.roles';
  // A shorter label for the phone tab bar, where space is tight.
  short?: 'nav.post' | 'nav.alerts' | 'nav.chats';
  // The icon shown next to the label, and alone when the sidebar is collapsed
  // (decorative).
  icon: IconName;
  // The feature nav badges render next to this entry (e.g. the unread count):
  // those of features that name no entry (FeatureSlots.navBadgeFor) and those
  // that name this one.
  badges?: boolean;
  // Who sees the entry (default: signed-in members).
  who?: NavAudience;
}

export const mainNav: MainNavEntry[] = [
  { to: '/', label: 'nav.feed', icon: 'feed' },
  { to: '/compose', label: 'nav.compose', icon: 'compose' },
  { to: '/invite', label: 'nav.invite', icon: 'invite' },
  { to: '/friends', label: 'nav.people', icon: 'people' },
  { to: '/saves', label: 'nav.saves', icon: 'saves' },
  { to: '/seen', label: 'nav.seen', icon: 'seen' },
  { to: '/notifications', label: 'nav.notifications', icon: 'notifications', badges: true },
  { to: '/messages', label: 'nav.messages', icon: 'messages', badges: true },
  { to: '/mod', label: 'nav.moderation', icon: 'moderation', who: 'moderator' },
  { to: '/settings', label: 'nav.settings', icon: 'settings' },
];

export const moreNav: MainNavEntry[] = [
  { to: '/about', label: 'nav.about', icon: 'about', who: 'everyone' },
  { to: '/terms', label: 'nav.terms', icon: 'terms', who: 'everyone' },
  { to: '/mod/log', label: 'nav.modLog', icon: 'mod-log' },
  { to: '/blame', label: 'nav.blame', icon: 'blame' },
  { to: '/mod/recovery', label: 'nav.modRecovery', icon: 'mod-recovery', who: 'moderator' },
  { to: '/mod/roles', label: 'nav.roles', icon: 'roles', who: 'admin' },
];

// tabNav is the phone tab bar: the pages people reach for most, with New post
// in the middle. Everything else is one tap away in the drawer.
export const tabNav: MainNavEntry[] = [
  { to: '/', label: 'nav.feed', icon: 'feed' },
  { to: '/friends', label: 'nav.people', icon: 'people' },
  { to: '/compose', label: 'nav.compose', short: 'nav.post', icon: 'compose' },
  { to: '/messages', label: 'nav.messages', short: 'nav.chats', icon: 'messages', badges: true },
  { to: '/notifications', label: 'nav.notifications', short: 'nav.alerts', icon: 'notifications', badges: true },
];

// navAllows reports whether a viewer sees an entry.
export function navAllows(e: MainNavEntry, signedIn: boolean, role: string | undefined): boolean {
  switch (e.who ?? 'member') {
    case 'everyone':
      return true;
    case 'member':
      return signedIn;
    case 'moderator':
      return signedIn && (role === 'moderator' || role === 'admin');
    case 'admin':
      return signedIn && role === 'admin';
  }
}
