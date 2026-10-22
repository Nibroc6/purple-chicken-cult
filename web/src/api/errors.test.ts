// Every error the UI can show renders with its arguments: no raw ICU template,
// whichever feature's key mapper picked the message, and no component may hand
// t an error key without the arguments (errorText or errorParams).
import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { i18n } from '../i18n/setup';
import en from '../i18n/locales/en/errors.json';
import { rawICU } from '../test/icu';
import { ApiError, errorKey, errorMsg, errorParams, errorText } from './errors';
import { postErrorKey } from '../features/post/vm';
import { authErrorKey } from '../features/auth/vm';
import { joinErrorKey } from '../features/join/vm';
import { invitesErrorKey } from '../features/invites/vm';
import { peopleErrorKey } from '../features/people/vm';
import { accountErrorKey } from '../features/settings/vm';
import { moderationErrorKey } from '../features/moderation/api';
import { recoveryErrorKey } from '../features/recovery/api';
import { treeErrorKey } from '../features/tree/vm';

const t = i18n.t.bind(i18n) as unknown as Parameters<typeof errorText>[0];

const mappers: [string, (e: unknown) => string | undefined][] = [
  ['errorKey', errorKey],
  ['postErrorKey', postErrorKey],
  ['authErrorKey', authErrorKey],
  ['joinErrorKey', joinErrorKey],
  ['invitesErrorKey', invitesErrorKey],
  ['peopleErrorKey', peopleErrorKey],
  ['accountErrorKey', accountErrorKey],
  ['moderationErrorKey', moderationErrorKey],
  ['recoveryErrorKey', recoveryErrorKey],
  ['treeErrorKey', treeErrorKey],
];

describe('error rendering', () => {
  it('formats rate_limited through every key mapper', () => {
    for (const retry of [undefined, 0.2, 1, 2, 30, 3600]) {
      const err = new ApiError(429, { error: 'rate_limited', retry_after_s: retry });
      for (const [name, keyFor] of mappers) {
        const text = errorText(t, err, keyFor);
        expect(rawICU(text), `${name} retry=${String(retry)}: ${text}`).toBeUndefined();
        expect(text).not.toMatch(/[{}]/);
      }
    }
    expect(errorText(t, new ApiError(429, { error: 'rate_limited', retry_after_s: 2 }))).toContain('2 seconds');
    expect(errorText(t, new ApiError(429, { error: 'rate_limited', retry_after_s: 1 }))).toContain('1 second.');
    expect(errorText(t, new ApiError(429, { error: 'rate_limited' }))).toBe(en.rate_limited_soon);
  });

  it('formats every shared error code, and a captured ErrorMsg the same way', () => {
    for (const code of Object.keys(en)) {
      const err = new ApiError(400, { error: code, retry_after_s: 3 });
      const text = errorText(t, err);
      expect(rawICU(text), code).toBeUndefined();
      expect(errorText(t, errorMsg(err))).toBe(text);
    }
  });

  it('rounds the retry hint up to whole seconds, at least one', () => {
    expect(errorParams(new ApiError(429, { error: 'rate_limited', retry_after_s: 0.2 }))).toEqual({ seconds: 1 });
    expect(errorParams(new ApiError(429, { error: 'rate_limited', retry_after_s: 2.1 }))).toEqual({ seconds: 3 });
    expect(errorParams(new Error('x'))).toEqual({});
  });
});

// Source scan: t(<anything>ErrorKey(...)) or t(<x>.key ...) without arguments is
// how the raw template reached the screen before; errorText is the one way.
function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((f) => {
    const p = path.join(dir, f);
    if (statSync(p).isDirectory()) return sources(p);
    return /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f) ? [p] : [];
  });
}

describe('error call sites', () => {
  it('never translate an error key without its arguments', () => {
    const root = path.resolve(__dirname, '..');
    const bad: string[] = [];
    for (const file of sources(root)) {
      const lines = readFileSync(file, 'utf8').split('\n');
      lines.forEach((line, i) => {
        const call = /\bt\(\s*(?:\w*[eE]rrorKey\(|\w+\.key\b|err\s+as\b|errKey\b)/.exec(line);
        if (call && !/errorParams\(|\.params\b|\.values\b/.test(line)) bad.push(`${path.relative(root, file)}:${String(i + 1)}: ${line.trim()}`);
      });
    }
    expect(bad).toEqual([]);
  });
});
