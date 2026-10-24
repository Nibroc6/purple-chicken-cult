// The pages only members can use (SCAFFOLD-owned list). Signed out, the shell
// shows a sign-in prompt in their place instead of the page's uniform 404s.
// "/" is the landing page when signed out, so it is not listed. A feature adds
// its member-only page here; a page left off keeps working as before.
const memberPrefixes = [
  '/compose',
  '/invite',
  '/friends',
  '/people',
  '/suggestions',
  '/saves',
  '/seen',
  '/notifications',
  '/settings',
  '/mod',
  '/blame',
  '/report',
  '/p',
  '/cosign',
  '/media',
];

// isMemberPath: the pathname is one of those pages (or below one).
export function isMemberPath(pathname: string): boolean {
  return memberPrefixes.some((p) => pathname === p || pathname.startsWith(`${p}/`));
}
