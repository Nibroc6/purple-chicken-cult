import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import sharedHosts from '../../../testdata/markdown/hosts.json';
import scanFixtures from '../../../testdata/markdown/scan.json';
import tableFixtures from '../../../testdata/markdown/tables.json';
import sharedTracking from '../../../testdata/tracking-params.json';
import { asRef, type PreviewVM } from '../vm/types';
import { displayHost, safeLink, stripTracking } from './host';
import { CODE_FOLD_LINES, extract, LinkPreview, Markdown, parse, tables, type MentionRef } from './index';
import tracking from './tracking-params.json';

function show(body: string, mentions?: MentionRef[]) {
  return render(<Markdown doc={parse(body, mentions)} />).container;
}

describe('shared fixtures with the server (testdata/markdown)', () => {
  it('extracts exactly the links and mentions mdscan extracts', () => {
    expect(scanFixtures.cases.length).toBeGreaterThanOrEqual(60);
    for (const c of scanFixtures.cases) {
      const got = extract(c.body);
      expect({ name: c.name, ...got }).toEqual({
        name: c.name,
        links: c.links.map((l) => ({ raw: l.raw, noPreview: l.no_preview })),
        mentions: c.mentions.map((m) => ({ handle: m.handle, ord: m.ord })),
      });
    }
  });

  it('finds exactly the tables, links and mentions mdscan finds in the table corpus', () => {
    expect(tableFixtures.cases.length).toBeGreaterThanOrEqual(40);
    for (const c of tableFixtures.cases) {
      const d = parse(c.body);
      expect({ name: c.name, links: d.links, mentions: d.mentions, tables: tables(d) }).toEqual({
        name: c.name,
        links: c.links.map((l) => ({ raw: l.raw, noPreview: l.no_preview })),
        mentions: c.mentions.map((m) => ({ handle: m.handle, ord: m.ord })),
        tables: c.tables,
      });
    }
  });

  it('decodes hosts and flags mixed scripts like urlx', () => {
    for (const c of sharedHosts.cases) {
      expect({ host: c.host, ...displayHost(c.host) }).toEqual({ host: c.host, unicode: c.unicode, lookalike: c.lookalike });
    }
  });

  it('embeds the shared tracking-parameter list', () => {
    expect(tracking).toEqual(sharedTracking);
  });
});

describe('tables', () => {
  it('renders a header row, body rows, alignments and inline content', () => {
    const el = show('| Who | Link | n |\n|:--|:-:|--:|\n| **Ana** | https://a.example | 3 |\n| Bo |');
    const table = el.querySelector('[data-testid="md-table"] table');
    expect(table).not.toBeNull();
    const ths = [...el.querySelectorAll('thead th')];
    expect(ths.map((th) => th.textContent)).toEqual(['Who', 'Link', 'n']);
    expect(ths.map((th) => th.getAttribute('scope'))).toEqual(['col', 'col', 'col']);
    expect(ths.map((th) => th.getAttribute('data-align'))).toEqual(['left', 'center', 'right']);
    const rows = [...el.querySelectorAll('tbody tr')];
    expect(rows.map((r) => [...r.querySelectorAll('td')].map((td) => td.textContent))).toEqual([
      ['Ana', 'https://a.example', '3'],
      ['Bo', '', ''],
    ]);
    expect(rows[0]?.querySelector('td strong')?.textContent).toBe('Ana');
    expect(rows[0]?.querySelector('td a')?.getAttribute('rel')).toBe('noopener noreferrer nofollow ugc');
    const wrap = el.querySelector('[data-testid="md-table"]');
    expect(wrap?.getAttribute('tabindex')).toBe('0');
    expect(wrap?.getAttribute('role')).toBe('region');
    expect(wrap?.getAttribute('aria-label')).toBeTruthy();
  });

  it('keeps over-wide, nested and pipe-less tables as text', () => {
    const wide = `|${' c |'.repeat(11)}\n|${'-|'.repeat(11)}`;
    for (const body of [wide, '> | a |\n> | - |', '- | a |\n  | - |', 'a | b\n--- | ---']) {
      const el = show(body);
      expect(el.querySelectorAll('table')).toHaveLength(0);
      expect(el.textContent).toContain('|');
    }
    expect(show(`|${' c |'.repeat(10)}\n|${'-|'.repeat(10)}`).querySelectorAll('thead th')).toHaveLength(10);
  });

  it('numbers mentions through table cells in document order', () => {
    const d = parse('@one\n\n| @two | x |\n| - | - |\n| @three | y |\n\n@four', [
      { ord: 0, ref: 'r0' },
      { ord: 2, ref: 'r2' },
    ]);
    expect(d.mentions.map((m) => m.handle)).toEqual(['one', 'two', 'three', 'four']);
    const el = render(<Markdown doc={d} />).container;
    expect([...el.querySelectorAll('[data-testid="md-mention"]')].map((a) => a.textContent)).toEqual(['@one', '@three']);
  });

  it('renders raw HTML in cells as text', () => {
    const el = show('| <img src=x onerror=alert(1)> |\n| - |');
    expect(el.querySelectorAll('img')).toHaveLength(0);
    expect(el.querySelector('th')?.textContent).toBe('<img src=x onerror=alert(1)>');
  });
});

describe('links', () => {
  it('protocol-checks hrefs: javascript:, data:, relative and userinfo links render as text', () => {
    const el = show('[click](javascript:alert(1)) [d](data:text/html,<b>x</b>) [rel](/home) [u](https://google.com@evil.example/)');
    expect(el.querySelectorAll('a')).toHaveLength(0);
    expect(el.querySelectorAll('[data-testid="md-nolink"]')).toHaveLength(4);
    expect(el.textContent).toContain('click');
    expect(el.textContent).not.toContain('javascript');
  });

  it('strips tracking parameters, drops titles and sends no referrer', () => {
    const el = show('[read](https://news.example/a?utm_source=x&id=7&fbclid=y "Title") and https://b.example/?gclid=1&q=2#top');
    const links = [...el.querySelectorAll('a')];
    expect(links.map((a) => a.getAttribute('href'))).toEqual(['https://news.example/a?id=7', 'https://b.example/?q=2#top']);
    for (const a of links) {
      expect(a.getAttribute('rel')).toBe('noopener noreferrer nofollow ugc');
      expect(a.getAttribute('referrerpolicy')).toBe('no-referrer');
      expect(a.hasAttribute('title')).toBe(false);
    }
  });

  it('shows the real host next to link text and decodes punycode with the mixed-script flag', () => {
    const el = show('[paypal.com](https://xn--pple-43d.com/login) https://xn--bcher-kva.example/ https://plain.example/');
    const hosts = [...el.querySelectorAll('[data-testid="md-host"]')];
    expect(hosts.map((h) => [h.textContent, h.getAttribute('data-lookalike')])).toEqual([
      ['(аpple.com · mixed scripts, check the address)', 'true'],
      ['(bücher.example)', 'false'],
    ]);
  });

  it('scheme-only autolinks: www. and bare domains stay text, <url> is a link', () => {
    const el = show('www.example.com example.com <https://quiet.example/x> HTTP://LOUD.EXAMPLE');
    const links = [...el.querySelectorAll('a')];
    expect(links.map((a) => [a.getAttribute('href'), a.getAttribute('data-kind')])).toEqual([
      ['https://quiet.example/x', 'auto'],
      ['http://loud.example/', 'bare'],
    ]);
    expect(parse('<https://quiet.example/x>').links).toEqual([{ raw: 'https://quiet.example/x', noPreview: true }]);
  });

  it('renders images as plain links and never loads them', () => {
    const el = show('![a cat](https://img.example/cat.png) ![](https://img.example/x.png)');
    expect(el.querySelectorAll('img')).toHaveLength(0);
    const links = [...el.querySelectorAll('a')];
    expect(links.map((a) => a.textContent)).toEqual(['a cat', 'https://img.example/x.png']);
    expect(links.map((a) => a.getAttribute('data-kind'))).toEqual(['image', 'image']);
  });
});

describe('mentions', () => {
  it('link only from server-issued refs, by ord', () => {
    const el = show('@alice and @bob and @carol', [{ ord: 1, ref: 'R-bob' }, { ord: 2 }]);
    const links = [...el.querySelectorAll('[data-testid="md-mention"]')];
    expect(links.map((a) => [a.textContent, a.getAttribute('href')])).toEqual([['@bob', '/people/R-bob']]);
    expect(el.textContent).toBe('@alice and @bob and @carol');
  });

  it('never link from text alone', () => {
    const el = show('[@alice](/people/x) `@bob` \\@carol');
    expect(el.querySelectorAll('[data-testid="md-mention"]')).toHaveLength(0);
    expect(el.querySelectorAll('a')).toHaveLength(0);
  });
});

describe('blocks', () => {
  it('a heading-only post renders as one paragraph-sized line', () => {
    const el = show('# Big news');
    expect(el.querySelectorAll('h1,h2,h3,h4,h5,h6')).toHaveLength(0);
    const p = el.querySelector('p.md-heading');
    expect(p?.textContent).toBe('Big news');
    expect(p?.getAttribute('data-md-level')).toBe('1');
  });

  it('shows raw HTML literally', () => {
    const el = show('<b>bold</b> <script>alert(1)</script>\n\n<div onclick="x()">block</div>');
    expect(el.querySelectorAll('b, script, div[onclick]')).toHaveLength(0);
    expect(el.textContent).toContain('<b>bold</b> <script>alert(1)</script>');
    expect(el.textContent).toContain('<div onclick="x()">block</div>');
  });

  it('keeps emphasis, strike (two tildes) and inline code', () => {
    const el = show('*em* **strong** ~~gone~~ ~kept~ `co<de>`');
    expect(el.querySelector('em')?.textContent).toBe('em');
    expect(el.querySelector('strong')?.textContent).toBe('strong');
    expect(el.querySelectorAll('s')).toHaveLength(1);
    expect(el.textContent).toContain('~kept~');
    expect(el.querySelector('code')?.textContent).toBe('co<de>');
  });

  it('nests blockquotes and lists at most two levels', () => {
    const el = show('> a\n> > b\n> > > c\n> > > > d\n\n- one\n  - two\n    - three\n      - four');
    expect(el.querySelectorAll('blockquote')).toHaveLength(2);
    expect(el.querySelectorAll('blockquote blockquote blockquote')).toHaveLength(0);
    expect(el.querySelectorAll('ul')).toHaveLength(2);
    expect(el.querySelectorAll('ul ul ul')).toHaveLength(0);
    for (const s of ['c', 'd', 'three', 'four']) expect(el.textContent).toContain(s);
  });

  it('keeps ordered list starts', () => {
    const el = show('3. three\n4. four');
    expect(el.querySelector('ol')?.getAttribute('start')).toBe('3');
  });

  it('collapses fenced code past 10 lines behind a keyboard toggle', () => {
    const lines = Array.from({ length: 14 }, (_, i) => `line ${String(i + 1)}`);
    const el = show('```js\n' + lines.join('\n') + '\n```\n\n```\nshort\n```');
    const blocks = screen.getAllByTestId('md-code');
    expect(blocks).toHaveLength(2);
    const first = blocks[0];
    expect(first?.getAttribute('data-collapsed')).toBe('true');
    expect(first?.querySelector('code')?.textContent.split('\n')).toHaveLength(CODE_FOLD_LINES);
    const toggle = screen.getByTestId('md-code-toggle');
    expect(toggle.textContent).toBe('Show all 14 lines');
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(toggle.getAttribute('aria-controls')).toBe(first?.querySelector('pre')?.id);
    fireEvent.click(toggle);
    expect(first?.querySelector('code')?.textContent.split('\n')).toHaveLength(14);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(el.querySelectorAll('[data-testid="md-code-toggle"]')).toHaveLength(1);
  });

  it('marks the body as user content', () => {
    expect(show('hi').querySelector('[data-user-content]')).not.toBeNull();
  });

  it('ignores a doc without nodes (old stub shape)', () => {
    const { container } = render(<Markdown doc={{ source: 'x', nodes: [] }} />);
    expect(container.textContent).toBe('');
  });
});

describe('host helpers', () => {
  it('safeLink accepts only absolute http(s)', () => {
    for (const bad of ['javascript:alert(1)', 'data:text/html,x', '/rel', 'ftp://x.example/', 'http://u:p@x.example/', 'https://']) {
      expect(safeLink(bad)).toBeNull();
    }
    expect(safeLink('HTTPS://Example.COM/a?utm_term=1')?.href).toBe('https://example.com/a');
  });

  it('stripTracking keeps order and encoding', () => {
    expect(stripTracking('?b=2&utm_x=1&a=%20&UTM_Y&fbclid=3&&c')).toBe('?b=2&a=%20&c');
    expect(stripTracking('?utm%5Fsource=1')).toBe('');
    expect(stripTracking('')).toBe('');
  });
});

describe('LinkPreview', () => {
  const base: PreviewVM = {
    url: 'https://news.example/a?id=1',
    host: 'news.example',
    hostUnicode: 'news.example',
    lookalike: false,
    title: 'A title',
    description: 'A description',
  };

  it('shows the real host, title and description with a safe href', () => {
    const { container } = render(<LinkPreview preview={{ ...base, url: 'https://news.example/a?id=1&utm_medium=x' }} />);
    const a = container.querySelector('a');
    expect(a?.getAttribute('href')).toBe('https://news.example/a?id=1');
    expect(a?.getAttribute('rel')).toBe('noopener noreferrer nofollow ugc');
    expect(a?.getAttribute('referrerpolicy')).toBe('no-referrer');
    expect(screen.getByTestId('md-preview-host').textContent).toBe('news.example');
    expect(container.textContent).toContain('A title');
    expect(container.textContent).toContain('A description');
    expect(screen.getByTestId('md-preview').getAttribute('aria-label')).toBe('Link preview from news.example');
  });

  it('flags a mixed-script host and never links a non-http URL', () => {
    const { container } = render(
      <LinkPreview preview={{ ...base, url: 'javascript:alert(1)', host: 'xn--pple-43d.com', hostUnicode: 'аpple.com', lookalike: true }} />,
    );
    expect(container.querySelector('a')).toBeNull();
    const host = screen.getByTestId('md-preview-host');
    expect(host.getAttribute('data-lookalike')).toBe('true');
    expect(host.textContent).toBe('(аpple.com · mixed scripts, check the address)');
  });

  it('renders mention refs carried as view-model refs', () => {
    const el = show('@dave', [{ ord: 0, ref: asRef('R1') }]);
    expect(el.querySelector('a')?.getAttribute('href')).toBe('/people/R1');
  });
});

// No HTML sinks anywhere in src/markdown (plan §6.1; Trusted Types pcc-forbid).
describe('no innerHTML', () => {
  const sources = import.meta.glob(['./*.ts', './*.tsx', '!./*.test.tsx', '!./*.test.ts'], {
    query: '?raw',
    import: 'default',
    eager: true,
  });
  it('scans every source file', () => {
    const names = Object.keys(sources);
    expect(names.length).toBeGreaterThanOrEqual(6);
    for (const [name, src] of Object.entries(sources)) {
      for (const sink of ['innerHTML', 'outerHTML', 'insertAdjacentHTML', 'dangerouslySetInnerHTML', 'document.write', 'eval(']) {
        expect(`${name}: ${src.includes(sink)}`).toBe(`${name}: false`);
      }
    }
  });
});
