// The browser tab title: "<page heading> | <app name>" (trial feedback). Pages
// don't set it themselves: the shell follows the routed page's first <h1>, so
// every page, lazily loaded or not, gets a title without code of its own.
import { useEffect, type RefObject } from 'react';

// pageTitle is the document title for heading (just name when there is none).
export function pageTitle(heading: string | null | undefined, name: string, format: (page: string) => string): string {
  const page = heading?.replace(/\s+/g, ' ').trim();
  return page ? format(page) : name;
}

// usePageTitle keeps document.title in step with the first <h1> inside root.
export function usePageTitle(root: RefObject<HTMLElement | null>, name: string, format: (page: string) => string): void {
  useEffect(() => {
    const el = root.current;
    if (!el) return;
    const update = () => {
      const next = pageTitle(el.querySelector('h1')?.textContent, name, format);
      if (document.title !== next) document.title = next;
    };
    update();
    const obs = new MutationObserver(update);
    obs.observe(el, { childList: true, subtree: true, characterData: true });
    return () => {
      obs.disconnect();
    };
  }, [root, name, format]);
}
