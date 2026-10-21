// The signed-in reader's stored theme, and applying tokens to an element under
// the accessibility switches on <html> (re-applied whenever they change).
// Owner: P1-W09.
import { useQuery } from '@tanstack/react-query';
import { useEffect, type RefObject } from 'react';
import { useMe } from '../../app/session';
import { fetchTheme, themeKey } from './api';
import { applyTheme, overridesOf, type ThemeTokens } from './theme';

const watched = ['data-contrast', 'data-text-scale', 'data-dyslexia-font'];

export function useStoredTheme() {
  const me = useMe();
  return useQuery({ queryKey: themeKey, queryFn: fetchTheme, enabled: me !== undefined, staleTime: 5 * 60_000 });
}

// useAppliedTheme keeps tokens applied to target (the document root when
// target is omitted) and clears them on unmount.
export function useAppliedTheme(tokens: ThemeTokens | undefined, target?: RefObject<HTMLElement | null>): void {
  useEffect(() => {
    const root = document.documentElement;
    const el = target ? target.current : root;
    if (!el) return;
    const apply = () => {
      applyTheme(el, tokens, overridesOf(root));
    };
    apply();
    const obs = new MutationObserver(apply);
    obs.observe(root, { attributes: true, attributeFilter: watched });
    return () => {
      obs.disconnect();
      applyTheme(el, undefined, overridesOf(root));
    };
  }, [tokens, target]);
}
