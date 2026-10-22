// The signed-in reader's stored theme, applying tokens to an element under
// the accessibility switches on <html> (re-applied whenever they change), and
// applying a theme's CSS (sheet.ts) with its images.
// Owner: P1-W09.
import { useQuery } from '@tanstack/react-query';
import { useEffect, useRef, useState, type RefObject } from 'react';
import { useMe } from '../../app/session';
import { signedURL } from '../media/urls';
import { fetchTheme, themeKey } from './api';
import { buildSheet, checkSheet, documentSheet, type Scope, type SheetTarget } from './sheet';
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

const none: ReadonlyMap<string, string> = new Map();

export interface SheetImage {
  name: string;
  ref: string;
}

// useImageURLs resolves a theme's images to signed display URLs, and again a
// minute before the earliest one expires (the cache in features/media renews it).
export function useImageURLs(images: readonly SheetImage[]): ReadonlyMap<string, string> {
  const [state, setURLs] = useState<{ key: string; urls: ReadonlyMap<string, string> }>(() => ({ key: '', urls: new Map() }));
  const key = JSON.stringify(images.map((i) => [i.name, i.ref]));
  useEffect(() => {
    let live = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const list = JSON.parse(key) as [string, string][];
    const load = () => {
      void Promise.allSettled(list.map(([, ref]) => signedURL(ref, 'display'))).then((got) => {
        if (!live) return;
        const next = new Map<string, string>();
        let soonest = Infinity;
        got.forEach((g, i) => {
          const name = list[i]?.[0];
          if (g.status === 'fulfilled' && name) {
            next.set(name, g.value.url);
            soonest = Math.min(soonest, g.value.expiresAt.getTime());
          }
        });
        setURLs({ key, urls: next });
        if (Number.isFinite(soonest)) timer = setTimeout(load, Math.max(30_000, soonest - Date.now() - 60_000));
      });
    };
    load();
    return () => {
      live = false;
      if (timer) clearTimeout(timer);
    };
  }, [key]);
  // URLs of an earlier image list never leak into a later one.
  return state.key === key ? state.urls : none;
}

// useAppliedSheet keeps a theme's CSS applied in scope ('app': the whole app;
// 'preview': the theme page's preview) and drops it under high contrast, on
// unmount, or when css is undefined. A sheet that fails checkSheet applies
// nothing.
export function useAppliedSheet(css: string | undefined, images: readonly SheetImage[], scope: Scope): void {
  const urls = useImageURLs(css ? images : []);
  const target = useRef<SheetTarget | null>(null);
  useEffect(() => {
    target.current ??= documentSheet();
    const t = target.current;
    const root = document.documentElement;
    const names = new Set(images.map((i) => i.name));
    const ok = css !== undefined && checkSheet(css, names) === undefined;
    const apply = () => {
      t.replace(ok && root.dataset.contrast !== 'high' ? buildSheet(css, urls, scope) : '');
    };
    apply();
    const obs = new MutationObserver(apply);
    obs.observe(root, { attributes: true, attributeFilter: ['data-contrast'] });
    return () => {
      obs.disconnect();
      t.replace('');
    };
  }, [css, images, urls, scope]);
  useEffect(
    () => () => {
      target.current?.clear();
      target.current = null;
    },
    [],
  );
}
