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

// Detection (trial feedback: detect the language instead of asking). Runs in
// the browser with no data sent anywhere: the writing system decides most
// non-Latin scripts, and Latin text is scored on common short words and
// letters. It returns null when it is unsure, so the caller keeps its default.

const scripts: [RegExp, string][] = [
  [/[぀-ヿ]/u, 'ja'],
  [/[가-힯ᄀ-ᇿ]/u, 'ko'],
  [/\p{Script=Han}/u, 'zh'],
  [/\p{Script=Arabic}/u, 'ar'],
  [/\p{Script=Hebrew}/u, 'he'],
  [/\p{Script=Devanagari}/u, 'hi'],
  [/\p{Script=Greek}/u, 'el'],
  [/\p{Script=Thai}/u, 'th'],
  [/\p{Script=Cyrillic}/u, 'ru'],
];

const cyrillic = ['ru', 'uk', 'bg', 'sr', 'mk', 'be', 'kk'];

const words: Record<string, string> = {
  en: "the and is are was to of in that it you for with this on be have not but they what we my your at just so i'm don't it's can will",
  es: 'el la los las de que y en es un una por con para no se lo como pero más está muy son al del yo mi también',
  fr: "le la les de des et est un une que pas pour dans ce qui sur avec je tu il elle nous vous mais très c'est du au",
  de: 'der die das und ist nicht ein eine ich du zu mit auf den dem sich auch es wir ihr aber sehr für von',
  it: 'il lo la gli le di che e è un una non per con sono ho mi ma anche questo come più del della',
  pt: 'o a os as de que e é um uma não para com em por mais mas eu você isso muito está do da são',
  nl: 'de het een en is van niet ik je dat op met voor zijn maar ook wat er we te',
  sv: 'och att det är en ett som på jag inte med för har till av men du vi den så',
  pl: 'i w na nie że jest to się z do jak co ale tak mnie jestem już czy dla',
  tr: 've bir bu da de ne için çok ben sen ile var değil ama gibi daha mi mı olan',
};

const letters: Record<string, string> = {
  es: 'ñ¿¡',
  pt: 'ãõ',
  de: 'ß',
  sv: 'å',
  pl: 'ąęłśżźćń',
  tr: 'ğşı',
};

const vocab = new Map<string, string[]>();
for (const [l, list] of Object.entries(words)) {
  for (const w of list.split(' ')) vocab.set(w, [...(vocab.get(w) ?? []), l]);
}

// mine returns the user's own tag for a detected language ("pt" → "pt-BR").
function mine(lang: string, languages: readonly string[] | undefined): string {
  return languages?.find((l) => primarySubtag(l) === lang) ?? lang;
}

export function detectLang(text: string, languages?: readonly string[]): string | null {
  const s = text.toLowerCase().replace(/https?:\/\/\S+|[@#][\w.-]+/gu, ' ');
  const nonLatin: Record<string, number> = {};
  let latin = 0;
  for (const ch of s) {
    if (/\p{Script=Latin}/u.test(ch)) {
      latin++;
      continue;
    }
    const hit = scripts.find(([re]) => re.test(ch));
    if (hit) nonLatin[hit[1]] = (nonLatin[hit[1]] ?? 0) + 1;
  }
  // Kana marks Japanese even beside kanji; Hangul marks Korean.
  const top = Object.entries(nonLatin).sort((a, b) => b[1] - a[1])[0];
  if (top && top[1] >= 2 && top[1] >= latin) {
    if (nonLatin.ja) return mine('ja', languages);
    if (top[0] === 'ru') {
      if (/[іїєґ]/u.test(s)) return mine('uk', languages);
      const own = languages?.find((l) => cyrillic.includes(primarySubtag(l)));
      return own ?? 'ru';
    }
    return mine(top[0], languages);
  }

  const tokens = s.replace(/’/gu, "'").match(/[\p{L}']+/gu) ?? [];
  if (tokens.length < 3) return null;
  const score: Record<string, number> = {};
  for (const w of tokens) for (const l of vocab.get(w) ?? []) score[l] = (score[l] ?? 0) + 1;
  for (const [l, set] of Object.entries(letters)) {
    for (const ch of s) if (set.includes(ch)) score[l] = (score[l] ?? 0) + 1;
  }
  // A language the user reads wins a near tie.
  const own = new Set((languages ?? []).map(primarySubtag));
  for (const l of Object.keys(score)) if (own.has(l)) score[l] = (score[l] ?? 0) + 0.5;
  const ranked = Object.entries(score).sort((a, b) => b[1] - a[1]);
  const [best, second] = ranked;
  if (!best || best[1] < 2 || (second && best[1] < second[1] * 1.5)) return null;
  return mine(best[0], languages);
}
