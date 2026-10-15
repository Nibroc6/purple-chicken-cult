// Generates the pseudo-locale en-XA from every en catalog (ARCHITECTURE §15.4):
// only literal text is transformed (accents, ~40% expansion, [ … ] brackets);
// ICU arguments, plurals and selects are preserved. Output is gitignored.
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { parse } from '@formatjs/icu-messageformat-parser';
import { printAST } from '@formatjs/icu-messageformat-parser/printer.js';
import { localesDir, readJSON, withLock, writeAtomic } from './lib.mjs';

const accents = {
  a: 'à', b: 'ƀ', c: 'ç', d: 'ð', e: 'ê', f: 'ƒ', g: 'ĝ', h: 'ĥ', i: 'î', j: 'ĵ', k: 'ķ', l: 'ļ', m: 'ɱ',
  n: 'ñ', o: 'ô', p: 'þ', q: 'ǫ', r: 'ŕ', s: 'š', t: 'ŧ', u: 'û', v: 'ṽ', w: 'ŵ', x: 'ẋ', y: 'ý', z: 'ž',
  A: 'Å', B: 'Ɓ', C: 'Ç', D: 'Ð', E: 'Ê', F: 'Ƒ', G: 'Ĝ', H: 'Ĥ', I: 'Î', J: 'Ĵ', K: 'Ķ', L: 'Ļ', M: 'Ṁ',
  N: 'Ñ', O: 'Ô', P: 'Þ', Q: 'Ǫ', R: 'Ŕ', S: 'Š', T: 'Ŧ', U: 'Û', V: 'Ṽ', W: 'Ŵ', X: 'Ẋ', Y: 'Ý', Z: 'Ž',
};

export function pseudoText(s) {
  let out = '';
  let letters = 0;
  for (const ch of s) {
    out += accents[ch] ?? ch;
    if (/\p{L}/u.test(ch)) letters++;
  }
  // ~40% expansion, added as padding to the last word.
  const pad = Math.ceil(letters * 0.4);
  return out + (pad > 0 ? '~'.repeat(pad) : '');
}

function transform(elements) {
  for (const el of elements) {
    if (el.type === 0 /* literal */) el.value = pseudoText(el.value);
    if (el.options) for (const opt of Object.values(el.options)) transform(opt.value);
    if (el.children) transform(el.children);
  }
}

export function pseudoMessage(msg) {
  const ast = parse(msg, { ignoreTag: true });
  transform(ast);
  return `[${printAST(ast)}]`;
}

function walk(obj) {
  const out = Array.isArray(obj) ? [] : {};
  for (const [k, v] of Object.entries(obj)) {
    out[k] = v !== null && typeof v === 'object' ? walk(v) : pseudoMessage(String(v));
  }
  return out;
}

export async function generatePseudo() {
  const en = path.join(localesDir, 'en');
  const xa = path.join(localesDir, 'en-XA');
  await withLock(async () => {
    for (const f of fs.readdirSync(en).filter((n) => n.endsWith('.json'))) {
      writeAtomic(path.join(xa, f), JSON.stringify(walk(readJSON(path.join(en, f))), null, 2) + '\n');
    }
  });
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await generatePseudo();
}
