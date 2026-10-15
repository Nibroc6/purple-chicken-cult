// Markdown subset (ARCHITECTURE §10.1.6). SCAFFOLD hand-off stub; owner P0-W09 →
// P1-W11: markdown-it with html:false, scheme-only linkify, a node allowlist
// emitting React elements, headings at body size, protocol-checked hrefs.
// Never innerHTML. The stub renders paragraphs as plain text.
import type { MarkdownDoc } from '../vm/types';

export function parse(body: string): MarkdownDoc {
  return { source: body, nodes: body.split(/\n{2,}/).filter((p) => p.trim() !== '') };
}

export function Markdown({ doc }: { doc: MarkdownDoc }) {
  return (
    <div className="post-body" data-user-content="">
      {doc.nodes.map((n, i) => (
        <p key={i}>{String(n)}</p>
      ))}
    </div>
  );
}
