#!/usr/bin/env node
// Ownership checks over scripts/ownership.json (plan §0.1–§0.2).
//   node scripts/ownership.mjs selftest         no path is owned by two WPs of one phase
//   node scripts/ownership.mjs check <wp-id>    the branch diff vs the scaffold base touches only owned paths
//   node scripts/ownership.mjs owner <path>     print the owners of a path
// Base for `check`: $PCC_SCAFFOLD_BASE, else `git merge-base HEAD main`.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const own = JSON.parse(fs.readFileSync(path.join(root, 'scripts', 'ownership.json'), 'utf8'));

function expandBraces(p) {
  const m = p.match(/\{([^{}]*)\}/);
  if (!m) return [p];
  return m[1].split(',').flatMap((alt) => expandBraces(p.slice(0, m.index) + alt + p.slice(m.index + m[0].length)));
}

function globToRegExp(g) {
  let re = '';
  for (let i = 0; i < g.length; i++) {
    const c = g[i];
    if (c === '*' && g[i + 1] === '*') {
      re += '.*';
      i++;
      if (g[i + 1] === '/') i++;
    } else if (c === '*') re += '[^/]*';
    else if (c === '?') re += '[^/]';
    else re += c.replace(/[.+^$()|[\]\\]/g, '\\$&');
  }
  return new RegExp(`^${re}$`);
}

function compile(patterns) {
  const inc = [];
  const exc = [];
  for (const p of patterns) {
    const neg = p.startsWith('!');
    for (const e of expandBraces(neg ? p.slice(1) : p)) (neg ? exc : inc).push(globToRegExp(e));
  }
  return (file) => inc.some((r) => r.test(file)) && !exc.some((r) => r.test(file));
}

function wps() {
  const out = [];
  for (const [phase, m] of Object.entries(own.phases)) {
    for (const [id, pats] of Object.entries(m)) out.push({ phase, id, pats, match: compile(pats) });
  }
  return out;
}

function git(args) {
  return execFileSync('git', args, { cwd: root, encoding: 'utf8' }).trim();
}

function repoFiles() {
  const files = git(['ls-files', '--cached', '--others', '--exclude-standard']).split('\n').filter(Boolean);
  return files.map((f) => f.replaceAll('\\', '/'));
}

function probes(pats) {
  return pats
    .filter((p) => !p.startsWith('!'))
    .flatMap(expandBraces)
    .map((p) => p.replace(/\*\*/g, 'x/y').replace(/\*/g, 'x').replace(/\?/g, '0'));
}

const [cmd, arg] = process.argv.slice(2);
if (cmd === 'selftest') {
  const all = wps();
  const candidates = new Set([...repoFiles(), ...all.flatMap((w) => probes(w.pats))]);
  const errors = [];
  for (const phase of Object.keys(own.phases)) {
    const ws = all.filter((w) => w.phase === phase && !w.id.startsWith('int-'));
    for (const f of candidates) {
      const owners = ws.filter((w) => w.match(f)).map((w) => w.id);
      if (owners.length > 1) errors.push(`phase ${phase}: ${f} is owned by ${owners.join(', ')}`);
    }
  }
  if (errors.length) {
    for (const e of errors) console.error(`ownership: ${e}`);
    process.exit(1);
  }
  console.log('ownership: no path is owned twice within a phase');
} else if (cmd === 'check') {
  const w = wps().find((x) => x.id === arg);
  if (!w) {
    console.error(`ownership: unknown work package ${arg}`);
    process.exit(2);
  }
  const base = process.env.PCC_SCAFFOLD_BASE || git(['merge-base', 'HEAD', 'main']);
  const changed = [
    ...git(['diff', '--name-only', `${base}...HEAD`]).split('\n'),
    ...git(['diff', '--name-only', 'HEAD']).split('\n'),
    ...git(['ls-files', '--others', '--exclude-standard']).split('\n'),
  ].filter(Boolean);
  const request = new RegExp(`^docs/contract-requests/${arg}-[^/]+\\.md$`);
  const bad = [...new Set(changed)].filter((f) => !w.match(f) && !request.test(f));
  if (bad.length) {
    for (const f of bad) console.error(`ownership: ${arg} does not own ${f} (file a docs/contract-requests/${arg}-<n>.md)`);
    process.exit(1);
  }
  console.log(`ownership: ${arg} touched only owned paths (${String(changed.length)} files)`);
} else if (cmd === 'owner') {
  const owners = wps().filter((w) => w.match(arg)).map((w) => `${w.id} (phase ${w.phase})`);
  const steward = compile(own.steward)(arg) ? ['steward'] : [];
  console.log([...steward, ...owners].join('\n') || 'unowned');
} else {
  console.error('usage: node scripts/ownership.mjs selftest | check <wp-id> | owner <path>');
  process.exit(2);
}
