// GFM tables (P1-W11): markdown-it's table rule rewritten to the stricter
// rules the server's mdscan applies too (internal/platform/mdscan/table.go;
// testdata/markdown/tables.json holds the cases both sides must agree on):
//
//   - top level only (never inside a blockquote or a list);
//   - the header row and the delimiter row start with "|" (after at most 3
//     spaces of indentation), so no other block can start on either line;
//   - the delimiter row is "|", "-", ":", spaces and tabs only, its cells are
//     :?-+:? and it has at most MAX_TABLE_COLS columns; the header row has
//     exactly as many cells;
//   - body rows follow until a blank line (after trim), or a line that starts
//     another block and may interrupt a paragraph (ATX heading, fence,
//     blockquote, thematic break, a list item that may interrupt a
//     paragraph), or a setext underline ("===" or "---" alone); an indented
//     line is a row;
//   - cells split on "|" not preceded by "\" (the backslash is removed, also
//     inside code spans), are trimmed, padded to the header's count and cut
//     past it, and parsed as inline content.
//
// A table that breaks any rule stays paragraph text. The table_open token's
// meta carries the column alignments.
import type { StateBlock } from 'markdown-it';

export const MAX_TABLE_COLS = 10;

export type Align = '' | 'left' | 'center' | 'right';

const SETEXT = /^ {0,3}(?:=+|-+)[ \t]*$/;
const DELIM_CELL = /^:?-+:?$/;

function getLine(state: StateBlock, line: number): string {
  const pos = (state.bMarks[line] ?? 0) + (state.tShift[line] ?? 0);
  return state.src.slice(pos, state.eMarks[line]);
}

// escapedSplit is markdown-it's: split on "|" unless the previous character
// is a backslash, which is then dropped.
function escapedSplit(str: string): string[] {
  const result: string[] = [];
  let current = '';
  let lastPos = 0;
  let isEscaped = false;
  for (let pos = 0; pos < str.length; pos++) {
    const ch = str.charCodeAt(pos);
    if (ch === 0x7c) {
      if (!isEscaped) {
        result.push(current + str.substring(lastPos, pos));
        current = '';
        lastPos = pos + 1;
      } else {
        current += str.substring(lastPos, pos - 1);
        lastPos = pos;
      }
    }
    isEscaped = ch === 0x5c;
  }
  result.push(current + str.substring(lastPos));
  return result;
}

function cells(lineText: string): string[] {
  const columns = escapedSplit(lineText);
  if (columns.length > 0 && columns[0] === '') columns.shift();
  if (columns.length > 0 && columns[columns.length - 1] === '') columns.pop();
  return columns;
}

function delimiter(state: StateBlock, line: number): Align[] | null {
  if ((state.sCount[line] ?? 0) - state.blkIndent >= 4) return null;
  const text = getLine(state, line);
  if (text.length < 2 || !text.startsWith('|')) return null;
  if (!/^[|\-: \t]+$/.test(text)) return null;
  const columns = text.split('|');
  const aligns: Align[] = [];
  for (let i = 0; i < columns.length; i++) {
    const t = (columns[i] ?? '').trim();
    if (!t) {
      if (i === 0 || i === columns.length - 1) continue;
      return null;
    }
    if (!DELIM_CELL.test(t)) return null;
    const left = t.startsWith(':');
    const right = t.endsWith(':');
    aligns.push(left && right ? 'center' : right ? 'right' : left ? 'left' : '');
  }
  if (aligns.length === 0 || aligns.length > MAX_TABLE_COLS) return null;
  return aligns;
}

// terminates reports whether line starts a block that may interrupt a
// paragraph (lists with the paragraph restrictions), or is a setext underline.
function terminates(state: StateBlock, line: number, endLine: number): boolean {
  const pos = (state.bMarks[line] ?? 0) + (state.tShift[line] ?? 0);
  const raw = state.src.slice(state.bMarks[line], state.eMarks[line]);
  if (pos < (state.eMarks[line] ?? 0) && SETEXT.test(raw)) return true;
  const old = state.parentType;
  state.parentType = 'paragraph';
  try {
    for (const rule of state.md.block.ruler.getRules('blockquote')) {
      if (rule(state, line, endLine, true)) return true;
    }
    return false;
  } finally {
    state.parentType = old;
  }
}

export function table(state: StateBlock, startLine: number, endLine: number, silent: boolean): boolean {
  if (state.level !== 0 || state.blkIndent !== 0) return false;
  if (startLine + 2 > endLine) return false;
  const aligns = delimiter(state, startLine + 1);
  if (!aligns) return false;
  if ((state.sCount[startLine] ?? 0) - state.blkIndent >= 4) return false;
  const headText = getLine(state, startLine).trim();
  if (!headText.startsWith('|')) return false;
  const head = cells(headText);
  if (head.length !== aligns.length) return false;
  if (silent) return true;

  const oldParentType = state.parentType;
  state.parentType = 'table';
  const open = state.push('table_open', 'table', 1);
  open.meta = { aligns };
  const tableLines: [number, number] = [startLine, 0];
  open.map = tableLines;
  const row = (kind: 'th' | 'td', columns: readonly string[], line: number) => {
    const tr = state.push('tr_open', 'tr', 1);
    tr.map = [line, line + 1];
    for (let i = 0; i < aligns.length; i++) {
      state.push(`${kind}_open`, kind, 1);
      const inline = state.push('inline', '', 0);
      inline.content = (columns[i] ?? '').trim();
      inline.children = [];
      state.push(`${kind}_close`, kind, -1);
    }
    state.push('tr_close', 'tr', -1);
  };
  state.push('thead_open', 'thead', 1);
  row('th', head, startLine);
  state.push('thead_close', 'thead', -1);
  let nextLine = startLine + 2;
  let body = false;
  for (; nextLine < endLine; nextLine++) {
    if ((state.sCount[nextLine] ?? 0) < state.blkIndent) break;
    if (terminates(state, nextLine, endLine)) break;
    const text = getLine(state, nextLine).trim();
    if (!text) break;
    if (!body) {
      state.push('tbody_open', 'tbody', 1);
      body = true;
    }
    row('td', cells(text), nextLine);
  }
  if (body) state.push('tbody_close', 'tbody', -1);
  state.push('table_close', 'table', -1);
  tableLines[1] = nextLine;
  state.parentType = oldParentType;
  state.line = nextLine;
  return true;
}
