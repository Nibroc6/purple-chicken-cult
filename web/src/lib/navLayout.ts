// The navigation layout (§15.1 display setting): a collapsible sidebar (the
// default) or a top header, a switch on <html> read by CSS like the
// accessibility switches.
export type NavLayout = 'sidebar' | 'header';
export const navLayouts: readonly NavLayout[] = ['sidebar', 'header'];

export function applyNavLayout(l: NavLayout, root: HTMLElement = document.documentElement): void {
  root.dataset.nav = l;
}

// The sidebar's collapsed state is a per-device convenience (localStorage).
const collapsedKey = 'pcc.sidebar.collapsed';

export function loadCollapsed(): boolean {
  try {
    return localStorage.getItem(collapsedKey) === '1';
  } catch {
    return false;
  }
}

export function saveCollapsed(c: boolean): void {
  try {
    localStorage.setItem(collapsedKey, c ? '1' : '0');
  } catch {
    // Storage blocked: the state lasts for this page only.
  }
}
