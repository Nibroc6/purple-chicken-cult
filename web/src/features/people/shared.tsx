// Shared pieces of the people pages. Owner: P0-W03 → P1-W03.
import { useMutation, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, NavLink } from 'react-router';
import { MoreMenu } from '../../lib/MoreMenu';
import { Chain, InviterLabel, ReportAction, type ReportTarget } from '../../integrity';
import type { PersonVM } from '../../vm/types';
import { requestCosign } from '../cosign';
import { useOpenReport } from '../moderation/ReportModal';
import { runAction, sessionCosigns, type Cosig, type PersonAction } from './api';
import { confirmActions, cosignPurpose, errorParams, peopleErrorKey, primaryActions, secondaryActions } from './vm';
import './people.css';
import { InfoTip } from '../../lib/InfoTip';

// peopleKey prefixes every query of this feature (any action refreshes them all).
export const peopleKey = ['people'] as const;

// ErrorText renders an API error from the people or errors catalogs.
export function ErrorText({ err }: { err: unknown }) {
  const { t } = useTranslation(['people', 'errors']);
  if (!err) return null;
  return (
    <p className="people-error" role="alert">
      {t(peopleErrorKey(err) as 'errors:unknown', errorParams(err))}
    </p>
  );
}

// useReport opens the report modal for a person or (through the invitee) their inviter.
export function useReport(): (target: ReportTarget) => void {
  const open = useOpenReport();
  return (target) => {
    open({ kind: target.kind === 'inviter' ? 'inviter' : 'person', ref: target.ref });
  };
}

// cosignsKey caches whether this session co-signs (outside peopleKey: actions never change it).
const cosignsKey = ['session-cosigns'] as const;

function cosigns(qc: QueryClient): Promise<boolean> {
  return qc.query({ queryKey: cosignsKey, queryFn: sessionCosigns, staleTime: Infinity });
}

// i18n-dynamic: people:action.
// i18n-dynamic: people:confirm.
// i18n-dynamic: people:cosign.
function ActionButton({ person, action }: { person: PersonVM; action: PersonAction }) {
  const { t } = useTranslation('people');
  const qc = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const m = useMutation({
    // A graph-changing action of a passkey session is co-signed first; a
    // cancelled dialog changes nothing.
    mutationFn: async () => {
      const purpose = cosignPurpose(action, person);
      let cosig: Cosig | undefined;
      if (purpose && (await cosigns(qc))) {
        const r = await requestCosign({
          purpose,
          target: person.ref,
          summary: t(`cosign.${action}` as 'cosign.request', { handle: person.handle }),
        });
        if (!r) return false;
        cosig = r;
      }
      await runAction(action, person.ref, cosig);
      return true;
    },
    onSuccess: async (done) => {
      if (!done) return;
      setConfirming(false);
      await qc.invalidateQueries({ queryKey: peopleKey });
    },
  });
  const label = t(`action.${action}` as 'action.request', { handle: person.handle });
  if (confirming) {
    return (
      <span className="people-confirm" role="group" aria-label={label}>
        <span>{t(`confirm.${action}` as 'confirm.block', { handle: person.handle })}</span>
        <button
          type="button"
          data-testid={`confirm-${action}`}
          disabled={m.isPending}
          onClick={() => {
            m.mutate();
          }}
        >
          {label}
        </button>
        <button
          type="button"
          onClick={() => {
            setConfirming(false);
          }}
        >
          {t('confirm.cancel')}
        </button>
        <ErrorText err={m.error} />
      </span>
    );
  }
  return (
    <>
      <button
        type="button"
        data-testid={`action-${action}`}
        data-keep-open={confirmActions.has(action) ? '' : undefined}
        disabled={m.isPending}
        onClick={() => {
          if (confirmActions.has(action)) setConfirming(true);
          else m.mutate();
        }}
      >
        {label}
      </button>
      <ErrorText err={m.error} />
    </>
  );
}

// PersonActions renders the friendship actions, then mute and block. With
// menu, mute, hide paths and block go in a ⋯ More menu (the person page),
// and extra (the report action) follows the buttons.
export function PersonActions({
  person,
  secondary = true,
  menu = false,
  extra,
}: {
  person: PersonVM;
  secondary?: boolean;
  menu?: boolean;
  extra?: ReactNode;
}) {
  const { t } = useTranslation('people');
  const primary = primaryActions(person);
  const rest = secondary ? secondaryActions(person) : [];
  if (primary.length === 0 && rest.length === 0 && !extra) return null;
  return (
    <div className="people-actions">
      {(menu ? primary : [...primary, ...rest]).map((a) => (
        <ActionButton key={a} person={person} action={a} />
      ))}
      {menu && rest.length > 0 ? (
        <MoreMenu label={t('action.more')} text={t('action.more')} testId="person-more">
          {rest.map((a) => (
            <ActionButton key={a} person={person} action={a} />
          ))}
        </MoreMenu>
      ) : null}
      {extra}
    </div>
  );
}

// PersonCard is one person: name, chain and inviter label (integrity
// elements), an optional extra line and the actions.
export function PersonCard({
  person,
  extra,
  secondary = false,
  children,
}: {
  person: PersonVM;
  extra?: ReactNode;
  secondary?: boolean;
  children?: ReactNode;
}) {
  const { t } = useTranslation('people');
  const report = useReport();
  return (
    <li className="people-card" data-testid="person-card">
      <div className="people-card-head">
        <Link to={`/people/${person.ref}`} className="people-name">
          {person.displayName ? <strong data-user-content="">{person.displayName}</strong> : null}
          <span className="muted">{t('handle', { handle: person.handle })}</span>
        </Link>
        {person.relation !== 'self' && !person.blocked ? (
          <Chain chain={person.chain} author={{ ref: person.ref, handle: person.handle }} />
        ) : null}
        <InviterLabel inviter={person.inviter} invitee={person.ref} onReport={report} />
        {extra}
      </div>
      {children ?? <PersonActions person={person} secondary={secondary} />}
    </li>
  );
}

// PeopleList renders cards, or the empty text.
export function PeopleList({ people, empty, emptyTip, render }: {
  people: PersonVM[];
  empty: string;
  emptyTip?: string;
  render?: (p: PersonVM) => ReactNode;
}) {
  if (people.length === 0)
    return (
      <p className="muted">
        {empty}
        {emptyTip ? <InfoTip>{emptyTip}</InfoTip> : null}
      </p>
    );
  return <ul className="people-list">{people.map((p) => render?.(p) ?? <PersonCard key={p.ref} person={p} />)}</ul>;
}

// PeopleNav links the people pages as tabs; the current one is marked
// (aria-current) and styled as selected.
export function PeopleNav() {
  const { t } = useTranslation('people');
  return (
    <nav className="people-nav" aria-label={t('nav.label')}>
      <NavLink to="/friends" end>
        {t('friends.title')}
      </NavLink>
      <NavLink to="/friends/requests">{t('requests.title')}</NavLink>
      <NavLink to="/suggestions">{t('suggestions.title')}</NavLink>
      <NavLink to="/settings/blocks">{t('blocks.title')}</NavLink>
    </nav>
  );
}

// ReportPerson is the report action for a person page.
export function ReportPerson({ person }: { person: PersonVM }) {
  const report = useReport();
  return <ReportAction target={{ kind: 'person', ref: person.ref }} onReport={report} />;
}
