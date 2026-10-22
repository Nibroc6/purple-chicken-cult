// The app's default icons: Font Awesome Free (icons CC BY 4.0, code MIT),
// self-hosted from the npm packages and tree-shaken, so no third-party request
// and no injected stylesheet (style-src 'self' stays strict). Each icon is an
// inline SVG in the current text colour, always decorative: the control around
// it carries the accessible name. Code names an icon by what it means, not by
// its Font Awesome name, and the SVG carries that name as data-icon, a stable
// hook for theme CSS (features/themes/sheet.ts), e.g.
//   .icon[data-icon=feed] { display: none }
//   .nav-icon:has([data-icon=feed]) { background: image(home) center / contain no-repeat }
import { faBookmark, faComment, faEyeSlash, faHeart } from '@fortawesome/free-regular-svg-icons';
import {
  faAnglesLeft,
  faAnglesRight,
  faBars,
  faBell,
  faBookmark as faBookmarkSolid,
  faCheck,
  faChevronLeft,
  faChevronRight,
  faCircleCheck,
  faCircleInfo,
  faCircleMinus,
  faCircleQuestion,
  faDiamond,
  faEye,
  faFeather,
  faFlag,
  faGear,
  faHeart as faHeartSolid,
  faHouse,
  faMinus,
  faPen,
  faPenToSquare,
  faPlus,
  faReply,
  faUserGroup,
  faUserPlus,
  faUsers,
  faXmark,
  type IconDefinition,
} from '@fortawesome/free-solid-svg-icons';
import './icon.css';

const icons = {
  // Main navigation (app/mainNav.ts).
  feed: faHouse,
  compose: faPenToSquare,
  invite: faFeather,
  people: faUserGroup,
  saves: faBookmark,
  seen: faEye,
  notifications: faBell,
  moderation: faFlag,
  settings: faGear,
  about: faCircleQuestion,
  // Controls.
  menu: faBars,
  close: faXmark,
  collapse: faAnglesLeft,
  expand: faAnglesRight,
  prev: faChevronLeft,
  next: faChevronRight,
  'zoom-in': faPlus,
  'zoom-out': faMinus,
  info: faCircleInfo,
  ok: faCheck,
  fail: faXmark,
  // Post actions (features/engage, formats/*): the -on icons mark a pressed toggle.
  like: faHeart,
  'like-on': faHeartSolid,
  save: faBookmark,
  'save-on': faBookmarkSolid,
  hide: faEyeSlash,
  'show-less': faCircleMinus,
  reply: faReply,
  replies: faComment,
  // Integrity elements (integrity/).
  tier: faDiamond,
  'tier-done': faCircleCheck,
  inviter: faUserPlus,
  audience: faUsers,
  edited: faPen,
  report: faFlag,
} satisfies Record<string, IconDefinition>;

export type IconName = keyof typeof icons;

// Icons that point along the reading direction, mirrored in right-to-left text.
const directional = new Set<IconName>(['collapse', 'expand', 'prev', 'next']);

export function Icon({ name, className }: { name: IconName; className?: string }) {
  const [width, height, , , path] = icons[name].icon;
  return (
    <svg
      className={className ? `icon ${className}` : 'icon'}
      data-icon={name}
      data-directional={directional.has(name) ? 'true' : undefined}
      viewBox={`0 0 ${width} ${height}`}
      aria-hidden="true"
      focusable="false"
    >
      {(Array.isArray(path) ? path : [path]).map((d) => (
        <path key={d} d={d} fill="currentColor" />
      ))}
    </svg>
  );
}
