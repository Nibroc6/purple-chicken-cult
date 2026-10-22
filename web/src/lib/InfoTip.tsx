// A toggletip: a small ⓘ button that shows a short explanation next to terse UI
// copy. It opens on click, tap, Enter or Space (and on mouse hover), closes on a
// second press, Escape, a press elsewhere or focus leaving it. The bubble is a
// live region, so screen readers announce the text when it opens. Pass `label`
// when one page has several, so each button has its own accessible name.
import { useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import './infotip.css';

const edge = 8; // px kept between the bubble and the viewport edge

export function InfoTip({ children, label, testId }: { children: ReactNode; label?: string; testId?: string }) {
  const { t } = useTranslation('common');
  const [open, setOpen] = useState(false);
  const [pinned, setPinned] = useState(false);
  const id = useId();
  const wrap = useRef<HTMLSpanElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const bubble = useRef<HTMLSpanElement>(null);

  const close = () => {
    setOpen(false);
    setPinned(false);
  };

  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      if (!wrap.current?.contains(e.target as Node)) close();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      close();
      button.current?.focus();
    };
    document.addEventListener('pointerdown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('pointerdown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  // Keep the bubble inside the viewport on narrow screens.
  useLayoutEffect(() => {
    const b = bubble.current;
    if (!b || !open) return;
    b.style.setProperty('--infotip-shift', '0px');
    const r = b.getBoundingClientRect();
    const vw = document.documentElement.clientWidth || window.innerWidth;
    let shift = 0;
    if (r.right > vw - edge) shift = vw - edge - r.right;
    if (r.left + shift < edge) shift = edge - r.left;
    b.style.setProperty('--infotip-shift', `${Math.round(shift)}px`);
  }, [open]);

  return (
    <span
      ref={wrap}
      className="infotip"
      data-testid={testId}
      onBlur={(e) => {
        if (!wrap.current?.contains(e.relatedTarget as Node | null)) close();
      }}
      onPointerLeave={(e) => {
        if (e.pointerType === 'mouse' && !pinned) setOpen(false);
      }}
    >
      <button
        ref={button}
        type="button"
        className="infotip-button"
        aria-label={label ?? t('infotip.label')}
        aria-expanded={open}
        aria-controls={id}
        onPointerEnter={(e) => {
          if (e.pointerType === 'mouse') setOpen(true);
        }}
        onClick={() => {
          if (open && pinned) close();
          else {
            setOpen(true);
            setPinned(true);
          }
        }}
      />
      <span ref={bubble} id={id} role="status" className="infotip-bubble">
        {open ? children : null}
      </span>
    </span>
  );
}
