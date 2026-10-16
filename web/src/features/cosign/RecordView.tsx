// The record a passkey is about to sign, field by field, plus the exact JSON
// (plan §6.4; ARCHITECTURE §3 Phase 1). Owner: P1-W01.
import { useTranslation } from 'react-i18next';
import { formatDateTime } from '../../lib/time';
import type { RecordVM } from './vm';

export function RecordView({ record }: { record: RecordVM }) {
  const { t, i18n } = useTranslation('cosign');
  return (
    <section className="cosign-record" data-testid="cosign-record" data-kind={record.rawKind}>
      <h3>
        {record.kind
          ? // i18n-dynamic: cosign:kinds.
            t(`kinds.${record.kind}`)
          : t('kinds.unknown', { kind: record.rawKind })}
      </h3>
      <dl>
        {record.fields.map((f) => (
          <div key={f.name} className="cosign-field">
            <dt>
              {f.known
                ? // i18n-dynamic: cosign:fields.
                  t(`fields.${f.known}`)
                : f.name}
            </dt>
            <dd dir="ltr">{f.value}</dd>
          </div>
        ))}
      </dl>
      {record.issuedAt ? (
        <p className="cosign-issued">{t('record.issuedAt', { when: formatDateTime(record.issuedAt, i18n.language) })}</p>
      ) : null}
      <details>
        <summary>{t('record.exact')}</summary>
        <pre dir="ltr" data-testid="cosign-exact">
          {record.exact}
        </pre>
      </details>
    </section>
  );
}
