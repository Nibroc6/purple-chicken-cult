// Markdown subset → a small serializable tree (ARCHITECTURE §10.1.6, plan
// §6.1). markdown-it is configured exactly like the server's mdscan view:
// CommonMark, html:false (raw HTML stays literal text), strikethrough with two
// tildes, no linkify (bare URLs come from the shared run scanner, scheme-only),
// every link destination kept as written (no normalizeLink, no validateLink:
// safeLink decides what becomes an href). Mentions and links are numbered in
// document order, as the server numbers them.
//
// Policy applied here, once, in the data layer:
//   - headings become paragraph-sized "heading" blocks;
//   - ![alt](url) becomes a plain link (images are never fetched);
//   - blockquotes and lists nest at most 2 levels (deeper content is lifted);
//   - <url> autolinks and images are marked no-preview;
//   - GFM tables only at the top level, with the stricter rules of table.ts;
//   - a mention links only when the server issued a ref for its ord;
//   - hrefs are protocol-checked and stripped of tracking parameters, titles
//     are dropped, everything else renders as text.
import MarkdownIt, { type Token } from 'markdown-it';
import { safeLink, type SafeLink } from './host';
import { isHTTP, scanRun } from './scan';
import { table, type Align } from './table';
import type { MarkdownDoc } from '../vm/types';

export type Inline =
  | { t: 'text'; v: string }
  | { t: 'em' | 'strong' | 's'; c: Inline[] }
  | { t: 'code'; v: string }
  | { t: 'br'; soft: boolean }
  // kind: explicit [text](url), auto <url>, bare URL in text, image ![alt](url)
  | { t: 'link'; kind: 'explicit' | 'auto' | 'bare' | 'image'; raw: string; link: SafeLink | null; c: Inline[] }
  | { t: 'mention'; ord: number; text: string; ref?: string };

export type Block =
  | { t: 'p'; c: Inline[] }
  | { t: 'heading'; level: number; c: Inline[] }
  | { t: 'quote'; c: Block[] }
  | { t: 'list'; ordered: boolean; start: number; items: Block[][] }
  | { t: 'code'; lines: string[] }
  | { t: 'hr' }
  // GFM table (top level only, ≤ MAX_TABLE_COLS columns): rows[0] is the header.
  | { t: 'table'; align: Align[]; rows: Inline[][][] };

export interface ScanLink {
  raw: string;
  noPreview: boolean;
}
export interface ScanMention {
  handle: string; // canonical (lowercase)
  ord: number;
}

// ParsedDoc is the MarkdownDoc this module produces.
export interface ParsedDoc extends MarkdownDoc {
  readonly nodes: readonly Block[];
  readonly links: readonly ScanLink[];
  readonly mentions: readonly ScanMention[];
}

// MentionRef is the server's MentionView: a ref only when the reader may follow it.
export interface MentionRef {
  ord: number;
  ref?: string;
}

export const MAX_NESTING = 2;

const md = new MarkdownIt('commonmark', { html: false, linkify: false, typographer: false });
md.enable('strikethrough');
md.block.ruler.at('table', table, { alt: ['paragraph', 'reference'] });
md.enable('table');
md.disable('text_join'); // keep escapes and entities apart: they never start a mention or URL
md.validateLink = () => true;
md.normalizeLink = (u) => u;
md.normalizeLinkText = (u) => u;

interface Frame {
  kind: 'root' | 'quote' | 'list' | 'item' | 'lifted';
  blocks: Block[];
  list?: Extract<Block, { t: 'list' }>;
}

class Builder {
  readonly links: ScanLink[] = [];
  readonly mentions: ScanMention[] = [];
  private readonly refs: ReadonlyMap<number, string>;

  constructor(refs: readonly MentionRef[] | undefined) {
    const m = new Map<number, string>();
    for (const r of refs ?? []) if (r.ref) m.set(r.ord, r.ref);
    this.refs = m;
  }

  blocks(tokens: readonly Token[]): Block[] {
    const root: Block[] = [];
    const stack: Frame[] = [{ kind: 'root', blocks: root }];
    let quotes = 0;
    let lists = 0;
    const top = (): Frame => stack[stack.length - 1] ?? { kind: 'root', blocks: root };
    let pendingHeading: number | undefined;
    let tbl: Extract<Block, { t: 'table' }> | undefined;
    let row: Inline[][] | undefined;
    for (const tok of tokens) {
      switch (tok.type) {
        case 'table_open': {
          const meta = tok.meta as { aligns: Align[] };
          tbl = { t: 'table', align: [...meta.aligns], rows: [] };
          top().blocks.push(tbl);
          break;
        }
        case 'table_close':
          tbl = undefined;
          break;
        case 'tr_open':
          row = [];
          tbl?.rows.push(row);
          break;
        case 'tr_close':
          row = undefined;
          break;
        case 'blockquote_open': {
          quotes++;
          if (quotes > MAX_NESTING) {
            stack.push({ kind: 'lifted', blocks: top().blocks });
          } else {
            const q: Block = { t: 'quote', c: [] };
            top().blocks.push(q);
            stack.push({ kind: 'quote', blocks: q.c });
          }
          break;
        }
        case 'bullet_list_open':
        case 'ordered_list_open': {
          lists++;
          if (lists > MAX_NESTING) {
            stack.push({ kind: 'lifted', blocks: top().blocks });
          } else {
            const ordered = tok.type === 'ordered_list_open';
            const start = Number(tok.attrGet('start') ?? 1);
            const l: Extract<Block, { t: 'list' }> = { t: 'list', ordered, start: Number.isFinite(start) ? start : 1, items: [] };
            top().blocks.push(l);
            stack.push({ kind: 'list', blocks: top().blocks, list: l });
          }
          break;
        }
        case 'list_item_open': {
          const f = top();
          if (f.kind === 'list' && f.list) {
            const item: Block[] = [];
            f.list.items.push(item);
            stack.push({ kind: 'item', blocks: item });
          } else {
            stack.push({ kind: 'lifted', blocks: f.blocks });
          }
          break;
        }
        case 'blockquote_close':
          quotes--;
          stack.pop();
          break;
        case 'bullet_list_close':
        case 'ordered_list_close':
          lists--;
          stack.pop();
          break;
        case 'list_item_close':
          stack.pop();
          break;
        case 'heading_open':
          pendingHeading = Number(tok.tag.slice(1)) || 1;
          break;
        case 'heading_close':
          pendingHeading = undefined;
          break;
        case 'inline': {
          const c = this.inlines(tok.children ?? [], true);
          if (row) {
            row.push(c);
            break;
          }
          top().blocks.push(pendingHeading === undefined ? { t: 'p', c } : { t: 'heading', level: pendingHeading, c });
          break;
        }
        case 'fence':
        case 'code_block':
          top().blocks.push({ t: 'code', lines: tok.content.replace(/\n$/, '').split('\n') });
          break;
        case 'hr':
          top().blocks.push({ t: 'hr' });
          break;
        default:
          // paragraph_open/close carry nothing; html_* never occur (html:false).
          break;
      }
    }
    return root;
  }

  // inlines converts inline tokens; scan=false inside link text and image alt
  // text, where nothing is recorded (no mentions, bare URLs or inner images).
  inlines(tokens: readonly Token[], scan: boolean): Inline[] {
    const out: Inline[] = [];
    const targets: Inline[][] = [out];
    let run: string[] = [];
    let lit: boolean[] = [];
    const target = (): Inline[] => targets[targets.length - 1] ?? out;
    const flush = () => {
      if (run.length === 0) return;
      this.flushRun(run, lit, target(), scan);
      run = [];
      lit = [];
    };
    for (let i = 0; i < tokens.length; i++) {
      const tok = tokens[i];
      if (!tok) continue;
      switch (tok.type) {
        case 'text':
          for (const c of tok.content) {
            run.push(c);
            lit.push(false);
          }
          break;
        case 'text_special': {
          // An escape that markdown-it decoded, or a character reference, is
          // literal; an escape it kept ("\q") is two ordinary characters.
          const literal = tok.info === 'entity' || tok.content !== tok.markup;
          for (const c of tok.content) {
            run.push(c);
            lit.push(literal);
          }
          break;
        }
        case 'softbreak':
        case 'hardbreak':
          flush();
          target().push({ t: 'br', soft: tok.type === 'softbreak' });
          break;
        case 'code_inline':
          flush();
          target().push({ t: 'code', v: tok.content });
          break;
        case 'em_open':
        case 'strong_open':
        case 's_open': {
          flush();
          const n: Inline = { t: tok.type === 'em_open' ? 'em' : tok.type === 'strong_open' ? 'strong' : 's', c: [] };
          target().push(n);
          targets.push(n.c);
          break;
        }
        case 'em_close':
        case 'strong_close':
        case 's_close':
          flush();
          if (targets.length > 1) targets.pop();
          break;
        case 'link_open': {
          flush();
          let depth = 1;
          let j = i + 1;
          for (; j < tokens.length; j++) {
            const tj = tokens[j];
            if (tj?.type === 'link_open') depth++;
            if (tj?.type === 'link_close' && --depth === 0) break;
          }
          const raw = String(tok.attrGet('href') ?? '');
          const auto = tok.markup === 'autolink';
          if (scan) this.link(raw, auto);
          target().push({
            t: 'link',
            kind: auto ? 'auto' : 'explicit',
            raw,
            link: safeLink(raw),
            c: this.inlines(tokens.slice(i + 1, j), false),
          });
          i = j;
          break;
        }
        case 'image': {
          flush();
          const raw = String(tok.attrGet('src') ?? '');
          if (scan) this.link(raw, true);
          const alt = this.inlines(tok.children ?? [], false);
          target().push({ t: 'link', kind: 'image', raw, link: safeLink(raw), c: alt.length > 0 ? alt : [{ t: 'text', v: raw }] });
          break;
        }
        default:
          flush();
          break;
      }
    }
    flush();
    return out;
  }

  private link(raw: string, noPreview: boolean) {
    if (isHTTP(raw)) this.links.push({ raw, noPreview });
  }

  private flushRun(run: string[], lit: boolean[], into: Inline[], scan: boolean) {
    const text = (from: number, to: number) => {
      if (to <= from) return;
      const v = run.slice(from, to).join('');
      const last = into[into.length - 1];
      if (last?.t === 'text') last.v += v;
      else into.push({ t: 'text', v });
    };
    if (!scan) {
      text(0, run.length);
      return;
    }
    let at = 0;
    for (const it of scanRun(run, lit)) {
      text(at, it.start);
      const v = run.slice(it.start, it.end).join('');
      if (it.kind === 'url') {
        this.links.push({ raw: v, noPreview: false });
        into.push({ t: 'link', kind: 'bare', raw: v, link: safeLink(v), c: [{ t: 'text', v }] });
      } else {
        const ord = this.mentions.length;
        this.mentions.push({ handle: v.slice(1).toLowerCase(), ord });
        const ref = this.refs.get(ord);
        into.push(ref ? { t: 'mention', ord, text: v, ref } : { t: 'mention', ord, text: v });
      }
      at = it.end;
    }
    text(at, run.length);
  }
}

// parse turns a post body into the tree every layout renders. mentions are the
// server's MentionViews of this post (none: every mention is plain text).
export function parse(body: string, mentions?: readonly MentionRef[]): ParsedDoc {
  const b = new Builder(mentions);
  const nodes = b.blocks(md.parse(body, {}));
  return { source: body, nodes, links: b.links, mentions: b.mentions };
}

// extract returns the links and mentions of a body (the composer's view and
// the shared-fixture check against the server).
export function extract(body: string): { links: ScanLink[]; mentions: ScanMention[] } {
  const d = parse(body);
  return { links: [...d.links], mentions: [...d.mentions] };
}

// TableShape is a table's alignments and cell texts (the shared fixtures in
// testdata/markdown/tables.json compare it with the server's mdscan.Table).
export interface TableShape {
  align: Align[];
  rows: string[][];
}

// plainText is the text of inline content: text, code, link and mention text.
export function plainText(nodes: readonly Inline[]): string {
  let out = '';
  for (const n of nodes) {
    switch (n.t) {
      case 'text':
      case 'code':
        out += n.v;
        break;
      case 'mention':
        out += n.text;
        break;
      case 'br':
        break;
      default:
        out += plainText(n.c);
    }
  }
  return out;
}

// tables returns the shape of every table of a parsed body, in order.
export function tables(doc: ParsedDoc): TableShape[] {
  const out: TableShape[] = [];
  for (const b of doc.nodes) {
    if (b.t === 'table') out.push({ align: [...b.align], rows: b.rows.map((r) => r.map(plainText)) });
  }
  return out;
}
