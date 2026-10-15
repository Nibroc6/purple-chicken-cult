// Post language (§10.7, D15): the guess is the first entry of the user's
// languages[], then the UI locale's primary subtag; "und" (mixed or unknown)
// and a manual choice are always available. Owner: P0-W05 → P1-W12.

// common is offered after the user's own languages.
const common = ['en', 'es', 'fr', 'de', 'it', 'pt', 'nl', 'sv', 'pl', 'uk', 'ru', 'tr', 'ar', 'he', 'hi', 'ja', 'ko', 'zh'];

export const UND = 'und';

// primarySubtag returns the language subtag of a BCP-47 tag ("pt-BR" → "pt").
export function primarySubtag(tag: string): string {
  return (tag.split(/[-_]/)[0] ?? '').toLowerCase();
}

export function defaultLang(languages: readonly string[] | undefined, uiLocale: string | undefined): string {
  const first = languages?.find((l) => l.trim() !== '');
  if (first) return first;
  const p = uiLocale ? primarySubtag(uiLocale) : '';
  return p !== '' ? p : UND;
}

// langOptions lists the user's languages first, then the current choice, then
// common languages, then "und"; no duplicates.
export function langOptions(languages: readonly string[] | undefined, current: string): string[] {
  const out: string[] = [];
  for (const l of [...(languages ?? []), current, ...common]) {
    if (l !== '' && l !== UND && !out.includes(l)) out.push(l);
  }
  out.push(UND);
  return out;
}

// langName names a language in the UI locale (falls back to the tag itself).
export function langName(tag: string, uiLocale: string): string {
  try {
    return new Intl.DisplayNames([uiLocale], { type: 'language' }).of(tag) ?? tag;
  } catch {
    return tag;
  }
}
