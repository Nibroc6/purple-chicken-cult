// The one modal every secondary view uses (likes, report, small confirmations).
// A native <dialog> opened with showModal(): the browser keeps focus inside it
// and makes the page behind inert, Escape closes it, focus goes back to where
// it was. A click on the backdrop closes it too. On phones it is a full-screen
// sheet (global.css). Which modal is open lives in the URL (useModalParam), so
// the back button closes it and a link can open it. Security-sensitive flows
// (passkey, sign-in, recovery, co-sign) keep their own dialogs.
import { useCallback, useEffect, useId, useRef, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation, useNavigate } from 'react-router';
import { Icon } from '../lib/Icon';

export interface ModalProps {
  title: ReactNode;
  onClose: () => void;
  children: ReactNode;
  testId?: string;
}

export function Modal({ title, onClose, children, testId }: ModalProps) {
  const { t } = useTranslation('common');
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();

  useEffect(() => {
    const d = ref.current;
    if (!d) return undefined;
    const before = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    if (!d.open) {
      // jsdom and very old browsers have no showModal.
      if (typeof d.showModal === 'function') d.showModal();
      else d.setAttribute('open', '');
    }
    return () => {
      if (d.open && typeof d.close === 'function') d.close();
      if (before?.isConnected) before.focus();
    };
  }, []);

  return (
    <dialog
      ref={ref}
      className="modal"
      aria-labelledby={titleId}
      data-testid={testId}
      onCancel={(e) => {
        // Escape: the URL decides what is open, so close through onClose.
        e.preventDefault();
        onClose();
      }}
      onKeyDown={(e) => {
        if (e.key !== 'Escape') return;
        e.preventDefault();
        onClose();
      }}
      onClick={(e) => {
        // A click on the ::backdrop lands on the dialog itself.
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="modal-body">
        <header className="modal-header">
          <h2 id={titleId}>{title}</h2>
          <button
            type="button"
            className="modal-close"
            aria-label={t('modal.close')}
            data-testid="modal-close"
            onClick={() => {
              onClose();
            }}
          >
            <Icon name="close" />
          </button>
        </header>
        {children}
      </div>
    </dialog>
  );
}

interface ModalState {
  modal?: string;
}

// useModalParam ties a modal to one search parameter of the current page
// (e.g. ?likes=<ref>): value is what the modal shows (null when closed), open
// pushes a history entry, close goes back to it (or, for a modal that came in
// with the page, drops the parameter in place).
export function useModalParam(name: string): [string | null, (value: string) => void, () => void] {
  const location = useLocation();
  const navigate = useNavigate();
  const { pathname, search, hash } = location;
  const state = location.state as ModalState | null;
  const value = new URLSearchParams(search).get(name);
  const open = useCallback(
    (v: string) => {
      const next = new URLSearchParams(search);
      next.set(name, v);
      void navigate({ pathname, search: `?${next.toString()}`, hash }, { state: { modal: name } satisfies ModalState });
    },
    [name, navigate, pathname, search, hash],
  );
  const close = useCallback(() => {
    if (state?.modal === name) {
      void navigate(-1);
      return;
    }
    const next = new URLSearchParams(search);
    next.delete(name);
    const rest = next.toString();
    void navigate({ pathname, search: rest ? `?${rest}` : '', hash }, { replace: true });
  }, [name, navigate, pathname, search, hash, state]);
  return [value, open, close];
}
