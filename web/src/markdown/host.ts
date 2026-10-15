// Real hosts and safe hrefs (plan §6.1, §6.6; security #23, #24). The client
// twin of internal/platform/urlx: punycode is decoded for display and a label
// is flagged "lookalike" when it mixes scripts or is written only with letters
// of another script that read as Latin (e.g. Cyrillic). This is mixed-script
// detection, not a full confusables check, and the UI says "mixed scripts".
// testdata/markdown/hosts.json holds the cases both sides must agree on.
import { decode, encode } from 'punycode/punycode.es6.js';
import tracking from './tracking-params.json';

export interface HostInfo {
  ascii: string; // the host as the browser will contact it
  unicode: string; // what we show
  lookalike: boolean;
}

// Scripts in the same order as urlx; a letter outside all of them is "other".
const SCRIPTS: readonly [string, RegExp][] = [
  'Latin',
  'Cyrillic',
  'Greek',
  'Armenian',
  'Georgian',
  'Cherokee',
  'Hebrew',
  'Arabic',
  'Devanagari',
  'Bengali',
  'Thai',
  'Ethiopic',
  'Han',
  'Hiragana',
  'Katakana',
  'Hangul',
  'Bopomofo',
].map((s) => [s, new RegExp(`^\\p{Script=${s}}$`, 'u')] as [string, RegExp]);

const LATIN_LIKE: Readonly<Record<string, string>> = {
  Cyrillic: 'аеорсухіјѕԁԛԝһӏ',
  Greek: 'οανικρυχϲϳ',
  Armenian: 'օսցհո',
  Cherokee: 'ꭺꭰꮃꮪꮯꮇꭼꮋꮲꮶ',
};

const ALLOWED_MIXES: readonly (readonly string[])[] = [
  ['Latin', 'Han', 'Hiragana', 'Katakana'],
  ['Latin', 'Han', 'Bopomofo'],
  ['Latin', 'Han', 'Hangul'],
];

const LETTER = /^\p{L}$/u;

function scriptOf(c: string): string {
  for (const [name, re] of SCRIPTS) if (re.test(c)) return name;
  return 'other';
}

// lookalike reports whether one host label mixes scripts or imitates Latin.
export function lookalike(label: string): boolean {
  const letters = Array.from(label).filter((c) => LETTER.test(c));
  const seen = new Set(letters.map(scriptOf));
  if (seen.size > 1) {
    return !ALLOWED_MIXES.some((mix) => [...seen].every((s) => mix.includes(s)));
  }
  for (const s of seen) {
    const set = LATIN_LIKE[s];
    if (set === undefined) return false;
    return letters.every((c) => set.includes(c));
  }
  return false;
}

// decodeLabel decodes one xn-- label strictly: it must decode, contain a
// non-ASCII character and encode back to itself (punycode.js alone is lenient).
function decodeLabel(l: string): string {
  if (!l.startsWith('xn--')) return l;
  const d = decode(l.slice(4));
  if (!Array.from(d).some((c) => (c.codePointAt(0) ?? 0) > 0x7f) || `xn--${encode(d)}` !== l) throw new Error('idn');
  return d;
}

const IPV4 = /^\d{1,3}(\.\d{1,3}){3}$/;

// displayHost decodes an ASCII host for display and flags lookalikes; a host
// that does not decode is shown as is and flagged.
export function displayHost(ascii: string): { unicode: string; lookalike: boolean } {
  const host = ascii.toLowerCase();
  if (IPV4.test(host) || host.startsWith('[')) return { unicode: host, lookalike: false };
  let labels: string[];
  try {
    labels = host.split('.').map(decodeLabel);
  } catch {
    return { unicode: host, lookalike: true };
  }
  return { unicode: labels.join('.'), lookalike: labels.some(lookalike) };
}

const EXACT = new Set(tracking.exact);
const PREFIXES = tracking.prefixes;

export function isTracking(key: string): boolean {
  const k = key.toLowerCase();
  return EXACT.has(k) || PREFIXES.some((p) => k.startsWith(p));
}

// stripTracking removes tracking parameters from a search string ("?a=1&…"),
// keeping the others as written and in order.
export function stripTracking(search: string): string {
  const q = search.startsWith('?') ? search.slice(1) : search;
  if (q === '') return '';
  const kept = q.split('&').filter((p) => {
    if (p === '') return false;
    const k = p.split('=', 1)[0] ?? '';
    let key = k;
    try {
      key = decodeURIComponent(k.replace(/\+/g, ' '));
    } catch {
      // keep the raw key
    }
    return !isTracking(key);
  });
  return kept.length > 0 ? `?${kept.join('&')}` : '';
}

export interface SafeLink {
  href: string;
  host: HostInfo;
}

// safeLink returns the href to render for a URL written in a post, or null
// when it must render as text: only absolute http(s) URLs without userinfo
// (new URL decides, not markdown-it's validateLink), tracking parameters
// stripped. Titles are never kept.
export function safeLink(raw: string): SafeLink | null {
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return null;
  }
  if ((u.protocol !== 'http:' && u.protocol !== 'https:') || u.username !== '' || u.password !== '' || u.hostname === '') {
    return null;
  }
  u.search = stripTracking(u.search);
  const ascii = u.hostname;
  return { href: u.href, host: { ascii, ...displayHost(ascii) } };
}

// needsHostBadge: a link whose text is not its own address always shows the
// real host; an address shown as written gets one only when its host is
// internationalized or a lookalike.
export function needsHostBadge(host: HostInfo, textIsURL: boolean): boolean {
  return !textIsURL || host.lookalike || host.unicode !== host.ascii;
}
