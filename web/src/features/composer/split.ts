// Split into a thread (§10.1.5): cut at paragraph, then sentence boundaries into
// at most 25 parts of about the soft limit each. Fenced code blocks are never
// split; lengths are code points and cuts never fall inside a grapheme (emoji,
// combining marks), so no part ever carries half a character. Owner: P0-W05 → P1-W12.
import { codePoints, MAX_PARTS, SOFT_LIMIT } from '../post/vm';

interface Piece {
  text: string;
  sep: string; // what joins it to the previous piece of the same part
}

const fence = /^ {0,3}(```|~~~)/;

// blocks returns the paragraphs of text; a fenced code block (blank lines and
// all) is one block, marked code.
export function blocks(text: string): { text: string; code: boolean }[] {
  const out: { text: string; code: boolean }[] = [];
  let cur: string[] = [];
  let open = '';
  const flush = (code: boolean) => {
    const s = cur.join('\n');
    if (s.trim() !== '') out.push({ text: s.replace(/^\n+|\s+$/g, ''), code });
    cur = [];
  };
  for (const line of text.split('\n')) {
    const m = fence.exec(line);
    if (open === '') {
      if (m) {
        flush(false);
        open = m[1] ?? '```';
        cur.push(line);
      } else if (line.trim() === '') {
        flush(false);
      } else {
        cur.push(line);
      }
    } else {
      cur.push(line);
      if (m && m[1] === open) {
        flush(true);
        open = '';
      }
    }
  }
  flush(open !== '');
  return out;
}

function segments(text: string, granularity: 'sentence' | 'word' | 'grapheme'): string[] {
  if (typeof Intl.Segmenter === 'function') {
    return Array.from(new Intl.Segmenter(undefined, { granularity }).segment(text), (s) => s.segment);
  }
  if (granularity === 'grapheme') return Array.from(text);
  if (granularity === 'word') return text.split(/(?<=\s)/);
  return text.split(/(?<=[.!?。！？]\s*)/);
}

// sentences splits a paragraph into sentences (keeping their trailing spaces).
export function sentences(par: string): string[] {
  return segments(par, 'sentence').filter((s) => s !== '');
}

// fit cuts s into chunks of at most limit code points at the finest boundary
// needed: words, then graphemes.
function fit(s: string, limit: number): string[] {
  if (codePoints(s) <= limit) return [s];
  for (const g of ['word', 'grapheme'] as const) {
    const units = segments(s, g);
    if (units.every((u) => codePoints(u) <= limit) || g === 'grapheme') {
      return greedy(units.map((u) => ({ text: u, sep: '' })), limit).map((p) => p.text);
    }
  }
  return [s];
}

function greedy(pieces: Piece[], limit: number): Piece[] {
  const out: Piece[] = [];
  let cur: Piece | undefined;
  for (const p of pieces) {
    if (cur && codePoints(cur.text) + codePoints(p.sep) + codePoints(p.text) <= limit) {
      cur.text += p.sep + p.text;
    } else {
      if (cur) out.push(cur);
      cur = { text: p.text, sep: p.sep };
    }
  }
  if (cur) out.push(cur);
  return out;
}

// pieces turns the text into units no longer than limit (code blocks whole),
// each with the separator that joins it to the previous unit.
function pieces(text: string, limit: number): Piece[] {
  const out: Piece[] = [];
  for (const b of blocks(text)) {
    if (b.code || codePoints(b.text) <= limit) {
      out.push({ text: b.text, sep: '\n\n' });
      continue;
    }
    const units = sentences(b.text).flatMap((s) => fit(s, limit));
    greedy(
      units.map((u) => ({ text: u, sep: '' })),
      limit,
    ).forEach((p, i) => {
      out.push({ text: p.text, sep: i === 0 ? '\n\n' : '' }); // sentences keep their own spacing
    });
  }
  return out;
}

// splitThread splits text into thread parts: about soft code points each (the
// target grows when 25 parts would not hold the text), never more than maxParts.
export function splitThread(text: string, soft = SOFT_LIMIT, maxParts = MAX_PARTS): string[] {
  const total = codePoints(text.trim());
  if (total === 0) return [];
  let limit = Math.max(soft, Math.ceil(total / maxParts));
  for (;;) {
    const parts = greedy(pieces(text, limit), limit)
      .map((p) => p.text.trim())
      .filter((p) => p !== '');
    if (parts.length <= maxParts) return parts;
    limit = Math.ceil(limit * 1.1);
  }
}

// joinParts merges thread parts back into one post.
export function joinParts(parts: readonly string[]): string {
  return parts
    .map((p) => p.trim())
    .filter((p) => p !== '')
    .join('\n\n');
}
