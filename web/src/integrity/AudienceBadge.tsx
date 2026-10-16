// Audience badge (§4, §15.5). Owner: P0-W10 → P1-W09. Who can see the post. A
// forced audience (probation, D6) is only ever sent to the author, who alone
// sees the note; for everyone else a forced d1 is byte-identical to a chosen one.
import { useTranslation } from 'react-i18next';
import type { AudienceBadgeProps } from './types';
import './integrity.css';

export function AudienceBadge({ audience, forced }: AudienceBadgeProps) {
  const { t } = useTranslation('integrity');
  return (
    <span
      className="integrity audience"
      data-testid="integrity-audience"
      data-audience={audience}
      data-forced={forced ? 'true' : undefined}
    >
      {t('audience.label', { audience })}
      {forced ? <span className="audience-forced">{t('audience.forced')}</span> : null}
    </span>
  );
}
