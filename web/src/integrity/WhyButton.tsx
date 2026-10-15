// "Why am I seeing this?" (SCAFFOLD hand-off stub). Owner: P0-W10 → P1-W09.
// Integrity element (§15.5): always rendered, visible and legible; reserved styles
// and icons are not available to user content.
import { useTranslation } from 'react-i18next';
import type { WhyButtonProps } from './types';

export function WhyButton({ post }: WhyButtonProps) {
  const { t } = useTranslation('integrity');
  return (
    <button type="button" className="integrity why" data-testid="integrity-why" data-post={post} aria-label={t('why.button')} />
  );
}
