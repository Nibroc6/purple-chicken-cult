// i18n setup (SCAFFOLD contract; plan §6.5): i18next + ICU MessageFormat +
// react-i18next. Namespaces load lazily from ./locales/<lng>/<ns>.json. The UI
// locale comes from MeView.settings.ui_locale, else navigator.languages; en-XA is
// the generated pseudo-locale (tests and screenshots).
import i18n, { type BackendModule, type ReadCallback } from 'i18next';
import ICU from 'i18next-icu';
import { initReactI18next } from 'react-i18next';
import { namespaces } from './namespaces';

const catalogs = import.meta.glob<{ default: Record<string, unknown> }>('./locales/*/*.json');

export const supportedLngs = ['en', 'en-XA'] as const;

const backend: BackendModule = {
  type: 'backend',
  init() {
    /* no options */
  },
  read(language: string, namespace: string, callback: ReadCallback) {
    const load = catalogs[`./locales/${language}/${namespace}.json`];
    if (!load) {
      callback(null, {});
      return;
    }
    load().then(
      (m) => {
        callback(null, m.default);
      },
      (err: unknown) => {
        callback(err instanceof Error ? err : new Error(String(err)), null);
      },
    );
  },
};

// pickLocale chooses the first supported locale among the candidates.
export function pickLocale(candidates: readonly string[]): string {
  const supported: readonly string[] = supportedLngs;
  for (const c of candidates) {
    if (supported.includes(c)) return c;
    const base = c.split('-')[0] ?? '';
    if (supported.includes(base)) return base;
  }
  return 'en';
}

export function initialLocale(): string {
  const fromQuery = new URLSearchParams(globalThis.location.search).get('lng');
  const nav = typeof navigator === 'undefined' ? [] : navigator.languages;
  return pickLocale([...(fromQuery ? [fromQuery] : []), ...nav]);
}

export async function setupI18n(lng: string = initialLocale()) {
  if (i18n.isInitialized) {
    if (i18n.language !== lng) await i18n.changeLanguage(lng);
    return i18n;
  }
  await i18n
    .use(ICU)
    .use(backend)
    .use(initReactI18next)
    .init({
      lng,
      fallbackLng: 'en',
      supportedLngs: [...supportedLngs],
      ns: ['common', 'errors'],
      defaultNS: 'common',
      partialBundledLanguages: true,
      interpolation: { escapeValue: false }, // React escapes
      react: { useSuspense: true },
    });
  document.documentElement.lang = lng;
  return i18n;
}

// allNamespaces is every declared namespace (tests preload them).
export const allNamespaces = [...namespaces];

export { i18n };
