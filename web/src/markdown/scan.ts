// The run scanner, mirrored line by line from internal/platform/mdscan/scan.go:
// bare scheme URLs and @mentions in one run of plain text. Change both
// together; testdata/markdown/scan.json catches any drift. A run is an array of
// code points (never UTF-16 units); lit[i] marks a character written as a
// backslash escape or a character reference, which never starts either item.

export interface RunItem {
  kind: 'url' | 'mention';
  start: number; // code-point index into the run
  end: number; // exclusive
}

export const MIN_HANDLE = 3;
export const MAX_HANDLE = 20;

export function scanRun(run: readonly string[], lit: readonly boolean[]): RunItem[] {
  const out: RunItem[] = [];
  let prev: string | undefined;
  for (let i = 0; i < run.length; ) {
    const c = run[i] ?? '';
    if ((c === 'h' || c === 'H') && !lit[i] && (prev === undefined || !isWord(prev))) {
      const j = schemeEnd(run, i);
      if (j > 0 && j < run.length && isHostStart(run[j] ?? '')) {
        let k = j;
        while (k < run.length && !isStop(run[k] ?? '')) k++;
        const end = trimURL(run, i, k);
        if (end > j) {
          out.push({ kind: 'url', start: i, end });
          prev = run[end - 1];
          i = end;
          continue;
        }
      }
    }
    if (c === '@' && !lit[i] && (prev === undefined || (!isWord(prev) && prev !== '@'))) {
      let j = i + 1;
      while (j < run.length && j - i - 1 <= MAX_HANDLE && isWord(run[j] ?? '')) j++;
      const n = j - i - 1;
      if (n >= MIN_HANDLE && n <= MAX_HANDLE && (j === run.length || !isWord(run[j] ?? ''))) {
        out.push({ kind: 'mention', start: i, end: j });
        prev = run[j - 1];
        i = j;
        continue;
      }
    }
    prev = c;
    i++;
  }
  return out;
}

const SCHEMES = ['https://', 'http://'];

// schemeEnd returns the index after "http://" or "https://" at i
// (case-insensitive), or 0.
function schemeEnd(run: readonly string[], i: number): number {
  for (const s of SCHEMES) {
    if (i + s.length > run.length) continue;
    let ok = true;
    for (let k = 0; k < s.length; k++) {
      if (lower(run[i + k] ?? '') !== s[k]) {
        ok = false;
        break;
      }
    }
    if (ok) return i + s.length;
  }
  return 0;
}

// isHTTP: an http(s) URL with something after the scheme that can start a host.
export function isHTTP(raw: string): boolean {
  const r = Array.from(raw);
  const j = schemeEnd(r, 0);
  return j > 0 && j < r.length && isHostStart(r[j] ?? '');
}

const TRAILING = new Set(['.', ',', ':', ';', '!', '?', "'", '"', '*', '_', '~']);

// trimURL drops trailing punctuation from run[start:end] (and a closing
// parenthesis or bracket without its opener), returning the new end.
function trimURL(run: readonly string[], start: number, end: number): number {
  while (end > start) {
    const c = run[end - 1] ?? '';
    if (TRAILING.has(c)) {
      end--;
      continue;
    }
    if (c === ')' || c === ']') {
      const open = c === ']' ? '[' : '(';
      let o = 0;
      let cl = 0;
      for (let k = start; k < end; k++) {
        if (run[k] === open) o++;
        else if (run[k] === c) cl++;
      }
      if (cl > o) {
        end--;
        continue;
      }
    }
    break;
  }
  return end;
}

function lower(c: string): string {
  return c >= 'A' && c <= 'Z' ? c.toLowerCase() : c;
}

// isWord: the ASCII word characters of handles.
export function isWord(c: string): boolean {
  return c === '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z');
}

function isHostStart(c: string): boolean {
  const cp = c.codePointAt(0) ?? 0;
  return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c === '[' || (cp >= 0x80 && !isSpace(c));
}

// isStop ends a bare URL: whitespace, controls, '<' and '>'.
function isStop(c: string): boolean {
  const cp = c.codePointAt(0) ?? 0;
  return cp < 0x20 || cp === 0x7f || c === '<' || c === '>' || isSpace(c);
}

const SPACE = /^\s$/u;

// isSpace is JavaScript's \s, which mdscan copies.
function isSpace(c: string): boolean {
  return SPACE.test(c);
}
