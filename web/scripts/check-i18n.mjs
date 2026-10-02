// i18n catalog check (plan §6.5): every en message parses as ICU; every namespace
// file is declared in src/i18n/namespaces.ts; no key is unused; en-XA has no key
// that en lacks. `node scripts/check-i18n.mjs [ns ...]` scopes the check to some
// namespaces (scripts/check-wp.sh passes the work package's own).
//
// A key K of namespace N counts as used when the source contains the literal
// 'N:K', or the literal 'K' in a file that calls useTranslation with N (or
// i18n.t with an explicit { ns: 'N' }). Dynamic keys are declared with a comment
// `// i18n-dynamic: N:prefix` (e.g. errors are looked up by API error code).
import fs from 'node:fs';
import path from 'node:path';
import { parse } from '@formatjs/icu-messageformat-parser';
import { flatten, localesDir, readJSON, webDir } from './lib.mjs';

const only = new Set(process.argv.slice(2));
const failures = [];
const fail = (msg) => failures.push(msg);

const nsSource = fs.readFileSync(path.join(webDir, 'src', 'i18n', 'namespaces.ts'), 'utf8');
const declared = new Set([...nsSource.matchAll(/'([a-z][a-zA-Z0-9-]*)'/g)].map((m) => m[1]));

function sources(dir, out = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) {
      if (e.name !== 'locales') sources(p, out);
    } else if (/\.(ts|tsx)$/.test(e.name) && !e.name.endsWith('.gen.ts')) {
      out.push(p);
    }
  }
  return out;
}

const files = sources(path.join(webDir, 'src')).map((p) => {
  const text = fs.readFileSync(p, 'utf8');
  const literals = new Set([...text.matchAll(/['"`]([^'"`\n]+)['"`]/g)].map((m) => m[1]));
  const nss = new Set();
  for (const m of text.matchAll(/useTranslation\(\s*(\[[^\]]*\]|'[^']*')/g)) {
    for (const n of m[1].matchAll(/'([^']+)'/g)) nss.add(n[1]);
  }
  for (const m of text.matchAll(/ns:\s*'([^']+)'/g)) nss.add(m[1]);
  const dynamic = [...text.matchAll(/i18n-dynamic:\s*([a-z][a-zA-Z0-9-]*):([A-Za-z0-9_.]*)/g)].map((m) => [m[1], m[2]]);
  return { literals, nss, dynamic };
});

const enDir = path.join(localesDir, 'en');
const en = {};
for (const f of fs.readdirSync(enDir).filter((n) => n.endsWith('.json'))) {
  const ns = f.replace(/\.json$/, '');
  if (!declared.has(ns)) fail(`en/${f}: namespace "${ns}" is not declared in src/i18n/namespaces.ts`);
  en[ns] = flatten(readJSON(path.join(enDir, f)));
}
for (const ns of declared) {
  if (!en[ns]) fail(`namespace "${ns}" has no en/${ns}.json`);
}

for (const [ns, keys] of Object.entries(en)) {
  if (only.size && !only.has(ns)) continue;
  for (const [key, msg] of Object.entries(keys)) {
    try {
      parse(msg, { ignoreTag: true });
    } catch (err) {
      fail(`${ns}:${key}: ICU parse error: ${err.message}`);
    }
    const used = files.some(
      (f) =>
        f.literals.has(`${ns}:${key}`) ||
        (f.nss.has(ns) && f.literals.has(key)) ||
        f.dynamic.some(([dns, prefix]) => dns === ns && key.startsWith(prefix)),
    );
    if (!used) fail(`${ns}:${key}: unused key`);
  }
}

const xaDir = path.join(localesDir, 'en-XA');
if (fs.existsSync(xaDir)) {
  for (const f of fs.readdirSync(xaDir).filter((n) => n.endsWith('.json'))) {
    const ns = f.replace(/\.json$/, '');
    const xa = flatten(readJSON(path.join(xaDir, f)));
    for (const key of Object.keys(xa)) {
      if (!en[ns] || !(key in en[ns])) fail(`en-XA/${f}: key ${key} does not exist in en`);
    }
  }
}

if (failures.length) {
  for (const f of failures) console.error(`check-i18n: ${f}`);
  process.exit(1);
}
console.log(`check-i18n: ${Object.keys(en).length} namespaces OK`);
