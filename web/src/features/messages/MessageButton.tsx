// The Message button of a person's page: opens (or starts) the private
// conversation with them. Owner: direct messages (contract request dm-1).
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import type { Ref } from '../../vm/types';
import { messagesKey, openDirect } from './api';
import { ErrorText } from './shared';

export function MessageButton({ person }: { person: Ref }) {
  const { t } = useTranslation('messages');
  const qc = useQueryClient();
  const navigate = useNavigate();
  const open = useMutation({
    mutationFn: () => openDirect(person),
    onSuccess: (c) => {
      void qc.invalidateQueries({ queryKey: messagesKey });
      void navigate(`/messages/${c.ref}`);
    },
  });
  return (
    <>
      <button
        type="button"
        data-testid="person-message"
        disabled={open.isPending}
        onClick={() => {
          open.mutate();
        }}
      >
        {t('person.action')}
      </button>
      <ErrorText err={open.error} />
    </>
  );
}
