// Theme CSS: the advanced mode of a theme (ARCHITECTURE §15.2 A, "Advanced
// themes"). It reads like CSS but is a closed dialect: a selector is a part
// name (optionally one state), a declaration an allowlisted property with a
// bounded value, and the only image source is image(name), one of the theme's
// own uploads. gate/themes (sheet.go) is the authority; this file mirrors its
// parser and contrast rule for the editor's live preview (both pass
// sheet.vectors.json), and builds the stylesheet from the parsed rules:
//
//   - Each part maps onto first-party selectors here, never onto anything the
//     user wrote. No selector matches an integrity element (or anything inside
//     one) or a locked surface (passkey and co-sign dialogs), and the app
//     drops the sheet on locked routes (sign-in, recovery, settings,
//     moderation, report), so a theme can never restyle a prompt that asks
//     for a passkey, and the theme page always works to undo a theme.
//   - The rules go into a constructed stylesheet through the CSSOM (no style
//     element or attribute: style-src 'self' stays strict), inside
//     @media not (forced-colors: active), and none apply under high contrast.
// Owner: P1-W09.
import type { ThemeColors } from './theme';
import { contrast } from './theme';

export const MAX_SHEET = 16 << 10;
export const MAX_RULES = 100;
export const MAX_DECLS = 24;
export const MAX_IMAGES = 8;
const MAX_SELECTORS = 8;
const PX_PER_EM = 16;
const AA = 4.5;

export type PartName =
  | 'page'
  | 'header'
  | 'brand'
  | 'nav'
  | 'card'
  | 'post-body'
  | 'link'
  | 'input'
  | 'button'
  | 'primary-button'
  | 'like'
  | 'save'
  | 'hide'
  | 'show-less';

interface PartInfo {
  button: boolean; // takes label (an image can replace the text)
  fg: 'text' | 'accent'; // the theme color of its text when a rule sets none
  sel: readonly string[]; // relative to the root; '' is the root itself
}

const parts: Record<PartName, PartInfo> = {
  page: { button: false, fg: 'text', sel: [''] },
  header: { button: false, fg: 'text', sel: ['.shell-header'] },
  brand: { button: true, fg: 'text', sel: ['.brand'] },
  nav: { button: true, fg: 'accent', sel: ['.nav-list a'] },
  card: { button: false, fg: 'text', sel: ['.post'] },
  'post-body': { button: false, fg: 'text', sel: ['.post-body'] },
  link: { button: false, fg: 'accent', sel: ['a'] },
  input: { button: false, fg: 'text', sel: ['input', 'textarea', 'select'] },
  button: { button: true, fg: 'text', sel: ['button'] },
  'primary-button': { button: true, fg: 'text', sel: ['button.primary'] },
  like: { button: true, fg: 'text', sel: ['[data-part="like"]'] },
  save: { button: true, fg: 'text', sel: ['[data-part="save"]'] },
  hide: { button: true, fg: 'text', sel: ['[data-part="hide"]'] },
  'show-less': { button: true, fg: 'text', sel: ['[data-part="show-less"]'] },
};

export const partNames = Object.keys(parts) as PartName[];

const stateSel = {
  hover: ':hover',
  focus: ':focus-visible',
  active: ':active',
  pressed: '[aria-pressed="true"]',
  current: '[aria-current="page"]',
  disabled: ':disabled',
} as const;
export type StateName = keyof typeof stateSel;
export const stateNames = Object.keys(stateSel) as StateName[];

// Never matched: integrity elements and what is inside them, and the
// dialogs that ask for a passkey or a co-signature.
const excluded = ':not(.integrity, .integrity *, .stepup-backdrop *, .cosign-backdrop *, .auth-second *)';

// Routes where the sheet is dropped (prefixes).
const lockedRoutes = ['/login', '/enroll', '/join', '/recover', '/cosign', '/settings', '/mod', '/report', '/blame', '/terms'];

export function isLockedRoute(path: string): boolean {
  return lockedRoutes.some((p) => path === p || path.startsWith(`${p}/`));
}

export interface Selector {
  part: PartName;
  state?: StateName;
}

export interface Decl {
  prop: string;
  value: string;
  line: number;
}

export interface Rule {
  selectors: Selector[];
  decls: Decl[];
  line: number;
}

export interface SheetError {
  code: 'theme_css' | 'theme_contrast';
  line: number; // 0: the sheet as a whole
}

export type Parsed = { ok: true; rules: Rule[] } | { ok: false; error: SheetError };

const sheetChars = /^[a-zA-Z0-9 \t\r\n#.,:;(){}%*/_-]*$/;
const nameRe = /^[a-z][a-z0-9-]*$/;
export const imageName = /^[a-z][a-z0-9-]{0,31}$/;
const hexShort = /^#[0-9a-f]{3}$/;
const hexLong = /^#[0-9a-f]{6}$/;
const numRe = /^(-?(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+))(px|rem|em|%|deg)?$/;

class Fault extends Error {
  constructor(
    readonly line: number,
    readonly code: SheetError['code'] = 'theme_css',
  ) {
    super(code);
  }
}

const isSpace = (c: string | undefined) => c === ' ' || c === '\t' || c === '\n' || c === '\r';

// parseSheet parses and validates src; images are the names image() may use.
export function parseSheet(src: string, images: ReadonlySet<string>): Parsed {
  try {
    return { ok: true, rules: parse(src, images) };
  } catch (e) {
    if (e instanceof Fault) return { ok: false, error: { code: e.code, line: e.line } };
    throw e;
  }
}

function parse(src: string, images: ReadonlySet<string>): Rule[] {
  if (src.length > MAX_SHEET || !sheetChars.test(src)) throw new Fault(firstBadLine(src));
  const clean = stripComments(src.toLowerCase());
  const lineAt = (off: number) => clean.slice(0, off).split('\n').length;
  const rules: Rule[] = [];
  let i = 0;
  for (;;) {
    while (i < clean.length && isSpace(clean[i])) i++;
    if (i === clean.length) break;
    const start = i;
    const open = indexAny(clean, '{};', i);
    if (open < 0 || clean[open] !== '{') throw new Fault(lineAt(start));
    const selectors = parseSelectors(clean.slice(i, open));
    if (!selectors) throw new Fault(lineAt(start));
    i = open + 1;
    const end = indexAny(clean, '{}', i);
    if (end < 0 || clean[end] !== '}') throw new Fault(lineAt(start));
    const rule: Rule = { selectors, decls: [], line: lineAt(start) };
    let off = i;
    for (const part of clean.slice(i, end).split(';')) {
      const line = lineAt(off + part.length - part.replace(/^[ \t\r\n]+/, '').length);
      off += part.length + 1;
      if (part.trim() === '') continue;
      const d = parseDecl(part, line, selectors, images);
      if (!d) throw new Fault(line);
      rule.decls.push(d);
    }
    i = end + 1;
    if (rule.decls.length > MAX_DECLS) throw new Fault(rule.line);
    checkLabel(rule);
    rules.push(rule);
    if (rules.length > MAX_RULES) throw new Fault(rule.line);
  }
  return rules;
}

function indexAny(s: string, chars: string, from: number): number {
  for (let j = from; j < s.length; j++) if (chars.includes(s[j] ?? '')) return j;
  return -1;
}

function firstBadLine(src: string): number {
  if (src.length > MAX_SHEET) return 0;
  let line = 1;
  for (const c of src) {
    if (c === '\n') {
      line++;
      continue;
    }
    if (!sheetChars.test(c)) return line;
  }
  return 0;
}

function stripComments(src: string): string {
  let out = '';
  const lineOf = (i: number) => src.slice(0, i).split('\n').length;
  for (let i = 0; i < src.length; ) {
    if (src.startsWith('/*', i)) {
      const end = src.indexOf('*/', i + 2);
      if (end < 0) throw new Fault(lineOf(i));
      out += src.slice(i, end + 2).replace(/[^\n]/g, ' ');
      i = end + 2;
      continue;
    }
    const c = src.charAt(i);
    if (c === '*' || c === '/') throw new Fault(lineOf(i));
    out += c;
    i++;
  }
  return out;
}

function parseSelectors(s: string): Selector[] | undefined {
  const out: Selector[] = [];
  for (const raw of s.split(',')) {
    const t = raw.trim();
    const colon = t.indexOf(':');
    const part = colon < 0 ? t : t.slice(0, colon);
    const state = colon < 0 ? undefined : t.slice(colon + 1);
    if (!(part in parts)) return undefined;
    if (state !== undefined && !(state in stateSel)) return undefined;
    out.push(state === undefined ? { part: part as PartName } : { part: part as PartName, state: state as StateName });
  }
  return out.length > 0 && out.length <= MAX_SELECTORS ? out : undefined;
}

function parseDecl(s: string, line: number, sels: Selector[], images: ReadonlySet<string>): Decl | undefined {
  const colon = s.indexOf(':');
  if (colon < 0) return undefined;
  const prop = s.slice(0, colon).trim();
  if (!nameRe.test(prop) || !Object.hasOwn(props, prop)) return undefined;
  const toks = lexValue(s.slice(colon + 1));
  if (!toks || toks.length === 0) return undefined;
  const value = props[prop]?.(toks, images);
  if (value === undefined) return undefined;
  if (prop === 'label' && !sels.every((x) => parts[x.part].button)) return undefined;
  return { prop, value, line };
}

// A rule that hides a button's text must give it an image.
function checkLabel(r: Rule): void {
  let hidden = 0;
  let image = false;
  for (const d of r.decls) {
    if (d.prop === 'label') hidden = d.value === 'hidden' ? d.line : 0;
    else if (d.prop === 'background-image') image = d.value.startsWith('image(');
  }
  if (hidden !== 0 && !image) throw new Fault(hidden);
}

// formatSheet is the normalized source (what the server stores).
export function formatSheet(rules: readonly Rule[]): string {
  return rules
    .map(
      (r) =>
        `${r.selectors.map((s) => (s.state ? `${s.part}:${s.state}` : s.part)).join(', ')} {\n${r.decls
          .map((d) => `  ${d.prop}: ${d.value};\n`)
          .join('')}}\n`,
    )
    .join('\n');
}

// sheetImages are the image names rules use.
export function sheetImages(rules: readonly Rule[]): Set<string> {
  const out = new Set<string>();
  for (const r of rules) {
    for (const d of r.decls) {
      const m = /^image\(([a-z0-9-]+)\)$/.exec(d.value);
      if (d.prop === 'background-image' && m?.[1]) out.add(m[1]);
    }
  }
  return out;
}

// checkSheet mirrors the server's contrast rule (gate/themes CheckSheet).
export function checkSheet(rules: readonly Rule[], c: ThemeColors): SheetError | undefined {
  for (const r of rules) {
    let fg: Decl | undefined;
    let bg: Decl | undefined;
    let hidden = false;
    for (const d of r.decls) {
      if (d.prop === 'color') fg = d;
      else if (d.prop === 'background-color') bg = d;
      else if (d.prop === 'label') hidden = d.value === 'hidden';
    }
    if (hidden || (!fg && !bg)) continue;
    const line = fg?.line ?? bg?.line ?? r.line;
    const bgs = bg && bg.value !== 'transparent' ? [bg.value] : [c.surface, c.bg];
    for (const s of r.selectors) {
      const f = fg?.value ?? (parts[s.part].fg === 'accent' ? c.accent : c.text);
      for (const b of bgs) {
        if (contrast(f.toLowerCase(), b.toLowerCase()) < AA) return { code: 'theme_contrast', line };
      }
    }
  }
  return undefined;
}

// --- values

type Tok =
  | { kind: 'i'; text: string }
  | { kind: 'n'; num: number; unit: string }
  | { kind: 'h'; text: string }
  | { kind: 'f'; text: string; args: Tok[] }
  | { kind: ',' };

function lexValue(s: string): Tok[] | undefined {
  const out: Tok[] = [];
  for (let i = 0; i < s.length; ) {
    const c = s[i];
    if (isSpace(c)) {
      i++;
    } else if (c === ',') {
      out.push({ kind: ',' });
      i++;
    } else if (c === '(' || c === ')') {
      return undefined;
    } else {
      let j = i;
      while (j < s.length && !isSpace(s[j]) && !',()'.includes(s[j] ?? '')) j++;
      const word = s.slice(i, j);
      if (s[j] === '(') {
        if (!nameRe.test(word)) return undefined;
        const end = s.indexOf(')', j);
        if (end < 0) return undefined;
        const inner = s.slice(j + 1, end);
        if (inner.includes('(')) return undefined;
        const args = lexValue(inner);
        if (!args) return undefined;
        out.push({ kind: 'f', text: word, args });
        i = end + 1;
        continue;
      }
      const t = word1(word);
      if (!t) return undefined;
      out.push(t);
      i = j;
    }
  }
  return out;
}

function word1(w: string): Tok | undefined {
  if (w.startsWith('#')) {
    if (hexShort.test(w)) return { kind: 'h', text: `#${w[1]}${w[1]}${w[2]}${w[2]}${w[3]}${w[3]}` };
    if (hexLong.test(w)) return { kind: 'h', text: w };
    return undefined;
  }
  if (nameRe.test(w)) return { kind: 'i', text: w };
  const m = numRe.exec(w);
  if (!m) return undefined;
  const n = Number(m[1]);
  if (!Number.isFinite(n)) return undefined;
  return { kind: 'n', num: n, unit: m[2] ?? '' };
}

function numText(t: { num: number; unit: string }): string {
  if (t.num === 0 && t.unit !== 'deg' && t.unit !== '%') return '0';
  return `${String(t.num)}${t.unit}`;
}

type Check = (toks: Tok[], images: ReadonlySet<string>) => string | undefined;

function colorTok(t: Tok | undefined, allowTransparent: boolean): string | undefined {
  if (t?.kind === 'h') return t.text;
  if (allowTransparent && t?.kind === 'i' && t.text === 'transparent') return t.text;
  return undefined;
}

const color =
  (allowTransparent: boolean): Check =>
  (toks) =>
    toks.length === 1 ? colorTok(toks[0], allowTransparent) : undefined;

const keywords =
  (...words: string[]): Check =>
  (toks) => {
    const t = toks[0];
    return toks.length === 1 && t?.kind === 'i' && words.includes(t.text) ? t.text : undefined;
  };

function lengthTok(t: Tok, lo: number, hi: number, pct: number): string | undefined {
  if (t.kind !== 'n') return undefined;
  switch (t.unit) {
    case 'px':
      if (t.num < lo || t.num > hi) return undefined;
      break;
    case 'rem':
    case 'em':
      if (t.num * PX_PER_EM < lo || t.num * PX_PER_EM > hi) return undefined;
      break;
    case '%':
      if (pct <= 0 || t.num < 0 || t.num > pct) return undefined;
      break;
    case '':
      if (t.num !== 0) return undefined;
      break;
    default:
      return undefined;
  }
  return numText(t);
}

const lengths =
  (n: number, lo: number, hi: number, pct: number, ...words: string[]): Check =>
  (toks) => {
    if (toks.length < 1 || toks.length > n) return undefined;
    const out: string[] = [];
    for (const t of toks) {
      if (t.kind === 'i' && words.includes(t.text)) {
        out.push(t.text);
        continue;
      }
      const v = lengthTok(t, lo, hi, pct);
      if (v === undefined) return undefined;
      out.push(v);
    }
    return out.join(' ');
  };

const backgroundImage: Check = (toks, images) => {
  const t = toks[0];
  if (toks.length !== 1 || !t) return undefined;
  if (t.kind === 'i' && t.text === 'none') return 'none';
  if (t.kind === 'f' && t.text === 'image') {
    const a = t.args[0];
    if (t.args.length !== 1 || a?.kind !== 'i' || !imageName.test(a.text) || !images.has(a.text)) return undefined;
    return `image(${a.text})`;
  }
  if (t.kind === 'f' && t.text === 'linear-gradient') {
    const groups: Tok[][] = [[]];
    for (const a of t.args) {
      if (a.kind === ',') groups.push([]);
      else groups[groups.length - 1]?.push(a);
    }
    const out: string[] = [];
    let colors = 0;
    for (const [i, g] of groups.entries()) {
      const x = g[0];
      if (g.length !== 1 || !x) return undefined;
      if (i === 0 && x.kind === 'n' && x.unit === 'deg') {
        if (x.num < 0 || x.num > 360) return undefined;
        out.push(numText(x));
        continue;
      }
      const c = colorTok(x, true);
      if (c === undefined) return undefined;
      out.push(c);
      colors++;
    }
    if (colors < 2 || colors > 6) return undefined;
    return `linear-gradient(${out.join(', ')})`;
  }
  return undefined;
};

const boxShadow: Check = (toks) => {
  const first = toks[0];
  if (toks.length === 1 && first?.kind === 'i' && first.text === 'none') return 'none';
  if (toks.length < 3 || toks.length > 5) return undefined;
  const c = colorTok(toks[toks.length - 1], true);
  if (c === undefined) return undefined;
  const bounds: [number, number][] = [
    [-32, 32],
    [-32, 32],
    [0, 64],
    [-16, 16],
  ];
  const out: string[] = [];
  for (const [i, t] of toks.slice(0, -1).entries()) {
    const b = bounds[i];
    const v = b ? lengthTok(t, b[0], b[1], 0) : undefined;
    if (v === undefined) return undefined;
    out.push(v);
  }
  return [...out, c].join(' ');
};

const fontWeight: Check = (toks) => {
  const t = toks[0];
  if (toks.length !== 1 || !t) return undefined;
  if (t.kind === 'i' && (t.text === 'normal' || t.text === 'bold')) return t.text;
  if (t.kind === 'n' && t.unit === '' && [400, 500, 600, 700, 800].includes(t.num)) return numText(t);
  return undefined;
};

const letterSpacing: Check = (toks) => {
  const t = toks[0];
  if (toks.length !== 1 || !t) return undefined;
  if (t.kind === 'i' && t.text === 'normal') return 'normal';
  if (t.kind === 'n' && (t.unit === 'em' || (t.unit === '' && t.num === 0)) && t.num >= -0.05 && t.num <= 0.3) return numText(t);
  return undefined;
};

const props: Record<string, Check> = {
  color: color(false),
  'background-color': color(true),
  'border-color': color(true),
  'background-image': backgroundImage,
  'background-size': lengths(2, 0, 2000, 100, 'cover', 'contain', 'auto'),
  'background-position': lengths(2, 0, 0, 100, 'center', 'top', 'bottom', 'left', 'right'),
  'background-repeat': keywords('no-repeat', 'repeat', 'repeat-x', 'repeat-y', 'space', 'round'),
  'border-width': lengths(1, 0, 8, 0),
  'border-style': keywords('none', 'solid', 'dashed', 'dotted', 'double'),
  'border-radius': lengths(1, 0, 999, 50),
  'box-shadow': boxShadow,
  'font-weight': fontWeight,
  'font-style': keywords('normal', 'italic'),
  'text-transform': keywords('none', 'uppercase', 'lowercase', 'capitalize'),
  'text-decoration': keywords('none', 'underline'),
  'letter-spacing': letterSpacing,
  'padding-block': lengths(2, 0, 48, 0),
  'padding-inline': lengths(2, 0, 48, 0),
  'min-block-size': lengths(1, 0, 240, 0),
  'min-inline-size': lengths(1, 0, 240, 0),
  label: keywords('visible', 'hidden'),
};

export const propNames = Object.keys(props);

// --- the stylesheet

export type Scope = 'app' | 'preview';

const roots: Record<Scope, string> = { app: '.shell', preview: '[data-theme-preview]' };

// Signed media URLs only: anything else (or a URL that could break out of
// url("…")) drops the declaration.
const safeURL = /^(https?:\/\/[^"'\\\s()<>]+|\/[^"'\\\s()<>]*)$/;

// buildRules turns parsed rules into CSS rule text for insertRule, one per
// rule. urls maps image names onto signed URLs; a declaration whose image has
// no URL (yet) is left out.
export function buildRules(rules: readonly Rule[], urls: ReadonlyMap<string, string>, scope: Scope): string[] {
  const root = roots[scope];
  const out: string[] = [];
  for (const r of rules) {
    const sels = r.selectors.flatMap((s) =>
      parts[s.part].sel.map((rel) => {
        const st = s.state ? stateSel[s.state] : '';
        return rel === '' ? `${root}${st}` : `${root} ${rel}${excluded}${st}`;
      }),
    );
    const decls: string[] = [];
    for (const d of r.decls) {
      const css = declCSS(d, urls);
      if (css) decls.push(...css);
    }
    if (decls.length === 0) continue;
    out.push(`@media not (forced-colors: active) { ${sels.join(', ')} { ${decls.join(' ')} } }`);
  }
  return out;
}

function declCSS(d: Decl, urls: ReadonlyMap<string, string>): string[] | undefined {
  if (d.prop === 'label') return d.value === 'hidden' ? ['font-size: 0;'] : undefined;
  if (d.prop === 'background-image') {
    const m = /^image\(([a-z0-9-]+)\)$/.exec(d.value);
    if (m?.[1]) {
      const url = urls.get(m[1]);
      if (!url || !safeURL.test(url)) return undefined;
      // Sensible defaults for an image on a button; later declarations win.
      return [`background-image: url("${url}");`, 'background-repeat: no-repeat;', 'background-position: center;', 'background-size: contain;'];
    }
  }
  return [`${d.prop}: ${d.value};`];
}

export interface SheetTarget {
  replace(rules: readonly string[]): void;
  clear(): void;
}

// documentSheet adopts one constructed stylesheet into doc (a no-op where
// the browser cannot construct stylesheets).
export function documentSheet(doc: Document = document): SheetTarget {
  let sheet: CSSStyleSheet | undefined;
  const ok = typeof CSSStyleSheet !== 'undefined' && 'adoptedStyleSheets' in doc;
  return {
    replace(rules) {
      if (!ok) return;
      if (!sheet) {
        try {
          sheet = new CSSStyleSheet();
        } catch {
          return;
        }
        doc.adoptedStyleSheets = [...doc.adoptedStyleSheets, sheet];
      }
      while (sheet.cssRules.length > 0) sheet.deleteRule(0);
      for (const r of rules) {
        try {
          sheet.insertRule(r, sheet.cssRules.length);
        } catch {
          // A rule the browser cannot parse is skipped alone.
        }
      }
    },
    clear() {
      if (!sheet) return;
      const s = sheet;
      doc.adoptedStyleSheets = doc.adoptedStyleSheets.filter((x) => x !== s);
      sheet = undefined;
    },
  };
}
