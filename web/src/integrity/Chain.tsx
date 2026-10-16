// Chain (§4.5, §15.5). Owner: P0-W10 → P1-W09. "you → @first → 2 others →
// @author": the first hop and the author always, middle hops only as the API
// names them (both endpoints of every named edge opted in), the rest collapsed
// into a count. Handles only, never display names.
import { useTranslation } from 'react-i18next';
import type { ChainVM } from '../vm/types';
import type { ChainProps } from './types';
import './integrity.css';

export function Chain({ chain, author }: ChainProps) {
  const { t } = useTranslation('integrity');
  return <ChainLine chain={chain} authorText={t('chain.handle', { handle: author.handle })} testId="integrity-chain" />;
}

// ChainLine renders a chain ending in authorText (the "why" panel names the
// author generically: its view has no handle).
export function ChainLine({ chain, authorText, testId }: { chain: ChainVM; authorText: string; testId: string }) {
  const { t } = useTranslation('integrity');
  const parts: string[] = [];
  if (chain.dist === 0) {
    parts.push(t('chain.self'));
  } else {
    parts.push(t('chain.you'));
    if (chain.firstHop && chain.dist > 1) parts.push(t('chain.handle', { handle: chain.firstHop.handle }));
    for (const h of chain.middle) parts.push(t('chain.handle', { handle: h.handle }));
    if (chain.hiddenMiddle > 0) parts.push(t('chain.hidden', { count: chain.hiddenMiddle }));
    parts.push(authorText);
  }
  // One text node, so the chain reads (and is found) as one phrase.
  const sep = t('chain.sep');
  const text = parts.join(` ${sep} `);
  return (
    <span
      className="integrity chain"
      role="group"
      data-testid={testId}
      data-dist={chain.dist}
      data-first-hop-plus={chain.firstHopPlus ? 'true' : undefined}
      aria-label={t('chain.label')}
    >
      {text}
    </span>
  );
}
