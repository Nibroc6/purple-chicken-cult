// Chain (SCAFFOLD hand-off stub). Owner: P0-W10 → P1-W09.
// Integrity element (§15.5): always rendered, visible and legible; reserved styles
// and icons are not available to user content.
import { useTranslation } from 'react-i18next';
import type { ChainProps } from './types';

export function Chain({ chain, author }: ChainProps) {
  const { t } = useTranslation('integrity');
  const hops = [chain.firstHop?.handle, ...chain.middle.map((h) => h.handle), author.handle].filter(Boolean);
  return (
    <span className="integrity chain" data-testid="integrity-chain" aria-label={t('chain.label')}>
      {hops.map((h) => `@${String(h)}`).join(' → ')}
      {chain.hiddenMiddle > 0 ? <span className="muted"> {t('chain.hidden', { count: chain.hiddenMiddle })}</span> : null}
    </span>
  );
}
