// A "More actions" (⋯) button that opens a small menu of secondary actions
// under it. The items stay in the DOM while closed (hidden), so a layout can
// always count on them. Escape, a click outside or picking an item closes it;
// Escape hands focus back to the button. Inside the menu, icon buttons show
// their label next to the icon (iconbutton.css), so every action is spelled
// out.
import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { Icon } from './Icon';
import './moremenu.css';

export function MoreMenu({ label, children, testId }: { label: string; children: ReactNode; testId?: string }) {
  const [open, setOpen] = useState(false);
  const id = useId();
  const root = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      if (root.current && e.target instanceof Node && !root.current.contains(e.target)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      setOpen(false);
      button.current?.focus();
    };
    document.addEventListener('pointerdown', onDown);
    document.addEventListener('keydown', onKey);
    root.current?.querySelector<HTMLElement>('.more-menu-list button')?.focus();
    return () => {
      document.removeEventListener('pointerdown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  return (
    <div className="more-menu" ref={root} data-open={open}>
      <button
        ref={button}
        type="button"
        className="icon-button more-menu-button"
        data-testid={testId}
        data-part="more"
        aria-label={label}
        title={label}
        aria-expanded={open}
        aria-controls={id}
        onClick={() => {
          setOpen((o) => !o);
        }}
      >
        <Icon name="more" />
      </button>
      <div
        id={id}
        className="more-menu-list"
        role="group"
        aria-label={label}
        hidden={!open}
        onClick={(e) => {
          // Picking an action closes the menu (the action runs first).
          if (e.target instanceof Element && e.target.closest('button')) setOpen(false);
        }}
      >
        {children}
      </div>
    </div>
  );
}
