// CSS must use logical properties so right-to-left scripts work without forks
// (ARCHITECTURE §15.4). Fails on physical left/right properties in src/**/*.css.
import fs from 'node:fs';
import path from 'node:path';
import { webDir } from './lib.mjs';

const banned = [
  /(^|[^-\w])margin-(left|right)\s*:/,
  /(^|[^-\w])padding-(left|right)\s*:/,
  /(^|[^-\w])border-(left|right)(-\w+)?\s*:/,
  /(^|[^-\w])(left|right)\s*:/,
  /text-align\s*:\s*(left|right)/,
  /float\s*:\s*(left|right)/,
  /clear\s*:\s*(left|right)/,
];

function walk(dir, out = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p, out);
    else if (e.name.endsWith('.css')) out.push(p);
  }
  return out;
}

const failures = [];
for (const file of walk(path.join(webDir, 'src'))) {
  fs.readFileSync(file, 'utf8')
    .split('\n')
    .forEach((line, i) => {
      const code = line.replace(/\/\*.*?\*\//g, '');
      if (banned.some((re) => re.test(code))) failures.push(`${path.relative(webDir, file)}:${i + 1}: ${line.trim()}`);
    });
}
if (failures.length) {
  for (const f of failures) console.error(`check-css-logical: physical property (use logical ones): ${f}`);
  process.exit(1);
}
console.log('check-css-logical: OK');
