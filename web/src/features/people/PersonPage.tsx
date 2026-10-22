// A person's page: who they are to you, how you are connected, who invited
// them, the friendship actions and (P1) the friend+ upgrade in person.
// Owner: P0-W03 → P1-W03.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams } from 'react-router';
import { Loading } from '../../app/Loading';
import { NotFound } from '../../app/NotFound';
import { ApiError } from '../../api/errors';
import { asRef } from '../../vm/types';
import { HandshakeHost, type OpenedVM } from '../invites';
import { fetchPerson, openUpgrade } from './api';
import { ErrorText, PeopleNav, PersonActions, PersonCard, ReportPerson, peopleKey } from './shared';
import { canUpgrade, type PeopleVM } from './vm';

// i18n-dynamic: people:person.relation.
export default function PersonPage() {
  const { t } = useTranslation('people');
  const ref = asRef(useParams().ref ?? '');
  const q = useQuery({ queryKey: [...peopleKey, 'person', ref], queryFn: () => fetchPerson(ref), retry: false });
  if (q.error instanceof ApiError && q.error.isNotFound) return <NotFound />;
  return (
    <section className="page people-page">
      <h1>{t('person.title')}</h1>
      <PeopleNav />
      {q.isPending ? (
        <Loading />
      ) : q.error ? (
        <ErrorText err={q.error} />
      ) : (
        <>
          <ul className="people-list">
            <PersonCard
              person={q.data}
              extra={
                <>
                  <span className="muted" data-testid="person-relation" data-relation={q.data.relation}>
                    {t(`person.relation.${q.data.blocked ? 'blocked' : q.data.request ? q.data.request : q.data.relation}` as 'person.relation.self')}
                  </span>
                  {q.data.hiddenVia ? (
                    <span className="muted" data-testid="person-hidden-via">
                      {t('person.hiddenVia', { handle: q.data.handle })}
                    </span>
                  ) : null}
                </>
              }
            >
              <PersonActions person={q.data} secondary />
              {q.data.relation !== 'self' ? <ReportPerson person={q.data} /> : null}
            </PersonCard>
          </ul>
          {q.data.relation !== 'self' ? <Upgrade key={q.data.ref} person={q.data} /> : null}
        </>
      )}
    </section>
  );
}

// Upgrade turns a plain friend into a friend+ in person (§7): this device
// shows the rotating code, the friend scans it from their own signed-in
// session at /join, both hear the sound and both co-sign. The panel stays
// once opened, so the outcome stays on screen after the upgrade.
function Upgrade({ person }: { person: PeopleVM }) {
  const { t } = useTranslation('people');
  const qc = useQueryClient();
  const titleId = useId();
  const [opened, setOpened] = useState<OpenedVM>();
  const [quiet, setQuiet] = useState(false);
  const open = useMutation({ mutationFn: () => openUpgrade(person.ref, quiet), onSuccess: setOpened });
  if (!opened && !canUpgrade(person)) return null;
  return (
    <section className="people-section people-upgrade" aria-labelledby={titleId} data-testid="upgrade-panel">
      <h2 id={titleId}>{t('upgrade.title')}</h2>
      {opened ? (
        <>
          <p>{t('upgrade.hint', { handle: person.handle })}</p>
          <HandshakeHost
            opened={opened}
            onDone={() => {
              void qc.invalidateQueries({ queryKey: peopleKey });
            }}
            onAgain={() => {
              setOpened(undefined);
              open.mutate();
            }}
          />
        </>
      ) : (
        <>
          <p>{t('upgrade.offer', { handle: person.handle })}</p>
          <label className="people-upgrade-quiet">
            <input
              type="checkbox"
              name="quiet"
              data-testid="upgrade-quiet"
              checked={quiet}
              onChange={(e) => {
                setQuiet(e.target.checked);
              }}
            />
            {t('upgrade.quiet')}
          </label>
          <button
            type="button"
            data-testid="action-upgrade"
            disabled={open.isPending}
            onClick={() => {
              open.mutate();
            }}
          >
            {t('upgrade.open')}
          </button>
        </>
      )}
      <ErrorText err={open.error} />
    </section>
  );
}
