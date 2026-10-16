// Report page (/report?kind=&ref=; §10.4). Owner: P0-W12 → P1-W06. Reports go
// to moderators only and never name the reporter. The server answers the same
// way whether or not the target exists or is visible, so the page only ever
// says "sent". kind=inviter carries the INVITEE's ref (the inviter is never
// addressable, §3).
import { useMutation } from '@tanstack/react-query';
import { useId, useState, type SyntheticEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate, useSearchParams } from 'react-router';
import {
  codePoints,
  defaultReason,
  errorParams,
  isRef,
  isTargetKind,
  moderationErrorKey,
  NoteMax,
  reportReasons,
  sendReport,
  type ReportReason,
  type TargetKind,
} from './api';
import './moderation.css';

export default function ReportPage() {
  const { t } = useTranslation('moderation');
  const [params] = useSearchParams();
  const kind = params.get('kind');
  const ref = params.get('ref');
  return (
    <section className="page moderation-page" data-testid="report-page">
      {isTargetKind(kind) && isRef(ref) ? (
        <ReportForm kind={kind} target={ref} />
      ) : (
        <>
          <h1>{t('report.title')}</h1>
          <p className="muted" data-testid="report-invalid">
            {t('report.invalid')}
          </p>
          <Link to="/">{t('report.home')}</Link>
        </>
      )}
    </section>
  );
}

function ReportForm({ kind, target }: { kind: TargetKind; target: string }) {
  const { t } = useTranslation(['moderation', 'errors']);
  const navigate = useNavigate();
  const noteId = useId();
  const [reason, setReason] = useState<ReportReason>(defaultReason(kind));
  const [note, setNote] = useState('');
  const send = useMutation({ mutationFn: () => sendReport(kind, target, reason, note) });
  const n = codePoints(note);
  const tooLong = n > NoteMax;
  const onSubmit = (e: SyntheticEvent) => {
    e.preventDefault();
    if (!tooLong) send.mutate();
  };
  const back = () => {
    void navigate(-1);
  };
  return (
    <>
      {/* i18n-dynamic: moderation:report.heading. */}
      <h1>{t(`report.heading.${kind}`)}</h1>
      {/* i18n-dynamic: moderation:report.about. */}
      <p className="muted">{t(`report.about.${kind}`)}</p>
      {send.isSuccess ? (
        <div className="moderation-done" data-testid="report-sent">
          <p role="status">{t('report.sent')}</p>
          <button type="button" onClick={back}>
            {t('report.back')}
          </button>
        </div>
      ) : (
        <form className="moderation-form" onSubmit={onSubmit} data-testid="report-form">
          <fieldset>
            <legend>{t('report.reasonLegend')}</legend>
            {reportReasons.map((r) => (
              <label key={r} className="moderation-choice">
                <input
                  type="radio"
                  name="reason"
                  value={r}
                  checked={reason === r}
                  onChange={() => {
                    setReason(r);
                  }}
                />
                <span>
                  {/* i18n-dynamic: moderation:reason. */}
                  <span className="moderation-reason">{t(`reason.${r}`)}</span>
                  {/* i18n-dynamic: moderation:reasonHint. */}
                  <small className="muted">{t(`reasonHint.${r}`)}</small>
                </span>
              </label>
            ))}
          </fieldset>
          <div className="moderation-field">
            <label htmlFor={noteId}>{t('report.note')}</label>
            <textarea
              id={noteId}
              rows={4}
              value={note}
              aria-invalid={tooLong}
              aria-describedby={`${noteId}-count`}
              onChange={(e) => {
                setNote(e.target.value);
              }}
            />
            <small id={`${noteId}-count`} className={tooLong ? 'moderation-over' : 'muted'} data-testid="report-count">
              {t('report.count', { count: n, max: NoteMax })}
            </small>
          </div>
          <p className="muted">{t('report.anonymous')}</p>
          <div className="moderation-actions">
            <button type="submit" disabled={send.isPending || tooLong} data-testid="report-submit">
              {t('report.submit')}
            </button>
            <button type="button" onClick={back}>
              {t('report.cancel')}
            </button>
          </div>
          {send.error ? (
            <p className="moderation-error" role="alert" data-testid="report-error">
              {t(moderationErrorKey(send.error) as 'errors:unknown', errorParams(send.error))}
            </p>
          ) : null}
        </form>
      )}
    </>
  );
}
