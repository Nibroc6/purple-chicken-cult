// Renders a parsed post body as React elements, never through an HTML string
// sink (Trusted Types pcc-forbid would throw anyway). Links carry rel="noopener noreferrer
// nofollow ugc", send no referrer and show their real host; hrefs exist only
// for http(s) (safeLink), otherwise the link's text renders as plain text.
// Headings are paragraph-sized semibold lines, fenced code folds past 10
// lines, mentions link only with a server-issued ref.
import { useId, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useInRouterContext } from 'react-router';
import type { MarkdownDoc } from '../vm/types';
import { needsHostBadge, type HostInfo } from './host';
import type { Block, Inline, ParsedDoc } from './parse';
import './markdown.css';

export const CODE_FOLD_LINES = 10;

export function Markdown({ doc }: { doc: MarkdownDoc }) {
  const nodes = (doc as Partial<ParsedDoc>).nodes ?? [];
  return (
    <div className="post-body md" data-user-content="">
      <Blocks blocks={nodes} />
    </div>
  );
}

function Blocks({ blocks }: { blocks: readonly Block[] }) {
  return (
    <>
      {blocks.map((b, i) => (
        <BlockView key={i} b={b} />
      ))}
    </>
  );
}

function BlockView({ b }: { b: Block }) {
  switch (b.t) {
    case 'p':
      return (
        <p>
          <Inlines nodes={b.c} />
        </p>
      );
    case 'heading':
      // Body size, semibold, no extra spacing: a heading-only post reads like
      // a normal paragraph (§10.1.6).
      return (
        <p className="md-heading" data-md-level={b.level}>
          <Inlines nodes={b.c} />
        </p>
      );
    case 'quote':
      return (
        <blockquote className="md-quote">
          <Blocks blocks={b.c} />
        </blockquote>
      );
    case 'list': {
      const items = b.items.map((item, i) => (
        <li key={i}>
          <Blocks blocks={item} />
        </li>
      ));
      return b.ordered ? (
        <ol className="md-list" start={b.start}>
          {items}
        </ol>
      ) : (
        <ul className="md-list">{items}</ul>
      );
    }
    case 'code':
      return <CodeBlock lines={b.lines} />;
    case 'hr':
      return <hr className="md-hr" />;
  }
}

// CodeBlock shows fenced or indented code; past 10 lines it is collapsed
// behind a real button (keyboard and screen readers unfold it too).
function CodeBlock({ lines }: { lines: string[] }) {
  const { t } = useTranslation('markdown');
  const id = useId();
  const [open, setOpen] = useState(false);
  const long = lines.length > CODE_FOLD_LINES;
  const shown = long && !open ? lines.slice(0, CODE_FOLD_LINES) : lines;
  return (
    <div className="md-code" data-testid="md-code" data-collapsed={long && !open}>
      <pre id={id}>
        <code>{shown.join('\n')}</code>
      </pre>
      {long ? (
        <button
          type="button"
          className="md-code-toggle"
          data-testid="md-code-toggle"
          aria-expanded={open}
          aria-controls={id}
          onClick={() => {
            setOpen((o) => !o);
          }}
        >
          {open ? t('code.showLess') : t('code.showAll', { count: lines.length })}
        </button>
      ) : null}
    </div>
  );
}

function Inlines({ nodes }: { nodes: readonly Inline[] }) {
  return (
    <>
      {nodes.map((n, i) => (
        <InlineView key={i} n={n} />
      ))}
    </>
  );
}

function InlineView({ n }: { n: Inline }) {
  switch (n.t) {
    case 'text':
      return <>{n.v}</>;
    case 'em':
      return (
        <em>
          <Inlines nodes={n.c} />
        </em>
      );
    case 'strong':
      return (
        <strong>
          <Inlines nodes={n.c} />
        </strong>
      );
    case 's':
      return (
        <s>
          <Inlines nodes={n.c} />
        </s>
      );
    case 'code':
      return <code className="md-code-inline">{n.v}</code>;
    case 'br':
      return <br />;
    case 'mention':
      return n.ref ? <MentionLink to={`/people/${n.ref}`}>{n.text}</MentionLink> : <>{n.text}</>;
    case 'link': {
      if (!n.link) {
        // Not http(s): the text, never an href (javascript:, data:, relative…).
        return (
          <span className="md-nolink" data-testid="md-nolink">
            <Inlines nodes={n.c} />
          </span>
        );
      }
      const textIsURL = n.kind === 'bare' || n.kind === 'auto';
      return (
        <>
          <a
            className="md-link"
            href={n.link.href}
            rel="noopener noreferrer nofollow ugc"
            referrerPolicy="no-referrer"
            target="_blank"
            data-testid="md-link"
            data-kind={n.kind}
          >
            <Inlines nodes={n.c} />
          </a>
          {needsHostBadge(n.link.host, textIsURL) ? <HostBadge host={n.link.host} /> : null}
        </>
      );
    }
  }
}

// HostBadge is the link's real host, decoded, with the mixed-script warning.
export function HostBadge({ host }: { host: HostInfo }) {
  const { t } = useTranslation('markdown');
  return (
    <span className="md-host" data-testid="md-host" data-lookalike={host.lookalike}>
      {host.lookalike ? t('link.lookalike', { host: host.unicode }) : t('link.host', { host: host.unicode })}
    </span>
  );
}

function MentionLink({ to, children }: { to: string; children: ReactNode }) {
  const inRouter = useInRouterContext();
  return inRouter ? (
    <Link to={to} className="md-mention" data-testid="md-mention">
      {children}
    </Link>
  ) : (
    <a href={to} className="md-mention" data-testid="md-mention">
      {children}
    </a>
  );
}
