// Markdown subset and link previews (ARCHITECTURE §10.1.6, plan §6.1, §6.6).
// Owner: P0-W09 → P1-W11.
//
// parse(body, mentions?) runs once in the data layer (features/post/vm.ts) and
// yields the tree every format renders with <Markdown doc={…} />; pass the
// post's server MentionViews so mentions the reader may follow become links.
// <LinkPreview> / <LinkPreviews> render server-built previews. No file here
// touches an HTML string sink (markdown.test.tsx scans them).
export { parse, extract, MAX_NESTING } from './parse';
export type { Block, Inline, MentionRef, ParsedDoc, ScanLink, ScanMention } from './parse';
export { Markdown, HostBadge, CODE_FOLD_LINES } from './Markdown';
export { LinkPreview, LinkPreviews } from './LinkPreview';
export { displayHost, lookalike, safeLink, stripTracking, isTracking } from './host';
export type { HostInfo, SafeLink } from './host';
