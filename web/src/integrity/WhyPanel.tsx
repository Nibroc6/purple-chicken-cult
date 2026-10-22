// The "why" panel (§5.3): the tier and chain a post came through, when it was
// posted, the order in use, how far ranking moved it and the reasons, all from
// catalogs. Owner: P0-W10 → P1-W09.
import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { Icon } from '../lib/Icon';
import { formatDateTime, formatRelative } from '../lib/time';
import { ChainLine } from './Chain';
import type { WhyVM } from './types';

export interface WhyPanelProps {
  id: string;
  loading: boolean;
  failed: boolean;
  why: WhyVM | undefined;
  onClose: () => void;
  onRetry: () => void;
}

// shiftArgs turns a signed minute count into the catalog's select/plural arguments.
export function shiftArgs(minutes: number): { dir: 'up' | 'down' | 'none'; unit: 'h' | 'm'; minutes: number; hours: number } {
  const abs = Math.abs(Math.round(minutes));
  return {
    dir: abs === 0 ? 'none' : minutes > 0 ? 'up' : 'down',
    unit: abs >= 60 ? 'h' : 'm',
    minutes: abs,
    hours: Math.round(abs / 60),
  };
}

export function WhyPanel({ id, loading, failed, why, onClose, onRetry }: WhyPanelProps) {
  const { t, i18n } = useTranslation('integrity');
  const root = useRef<HTMLDivElement>(null);
  useEffect(() => {
    root.current?.focus();
  }, []);
  return (
    <div
      ref={root}
      id={id}
      className="integrity why-panel"
      role="dialog"
      aria-modal="false"
      aria-label={t('why.title')}
      tabIndex={-1}
      data-testid="integrity-why-panel"
      onKeyDown={(e) => {
        if (e.key === 'Escape') {
          e.stopPropagation();
          onClose();
        }
      }}
    >
      <button type="button" className="why-close" data-testid="integrity-why-close" aria-label={t('why.close')} onClick={onClose}>
        <Icon name="close" />
      </button>
      <h3>{t('why.title')}</h3>
      {loading ? (
        <p aria-live="polite" data-testid="integrity-why-loading">
          {t('why.loading')}
        </p>
      ) : null}
      {failed ? (
        <p role="alert">
          {t('why.failed')}{' '}
          <button type="button" onClick={onRetry}>
            {t('why.retry')}
          </button>
        </p>
      ) : null}
      {why ? (
        <div data-testid="integrity-why-body">
          <p data-testid="integrity-why-tier">{t('why.tier', { tier: why.tier, tie: why.via.firstHopPlus ? 'friend' : 'acquaintance' })}</p>
          {why.tier > 0 ? <ChainLine chain={why.via} authorText={t('why.author')} testId="integrity-why-chain" /> : null}
          <p>
            {t('why.posted')}{' '}
            <time dateTime={why.postedAt.toISOString()} title={formatDateTime(why.postedAt, i18n.language)}>
              {formatRelative(why.postedAt, i18n.language)}
            </time>
          </p>
          <p data-testid="integrity-why-preset">{t('why.preset', { preset: why.preset })}</p>
          <p data-testid="integrity-why-shift">{t('why.shift', shiftArgs(why.shiftMinutes))}</p>
          {why.reasons.length > 0 ? (
            <ul data-testid="integrity-why-reasons">
              {why.reasons.map((r) => (
                // i18n-dynamic: integrity:why.reason.
                <li key={r} data-reason={r}>
                  {t(`why.reason.${r}`)}
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
