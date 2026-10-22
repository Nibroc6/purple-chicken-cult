// An icon-only button with a tooltip: the label is the button's accessible name
// (aria-label) and shows as a small tip on hover and keyboard focus, so sighted
// mouse and keyboard users see the same words a screen reader announces. An
// optional hint adds a second line, also the button's description. Extra
// children (a count) render after the icon. data-part and other attributes pass
// through, so theme CSS can restyle or replace the button (features/themes/sheet.ts).
// The tip is a manual popover: it renders in the top layer, above every card,
// clipped box and sticky header, placed above the button (below it near the
// top edge) and kept inside the viewport. Without popover support it falls back
// to an absolutely positioned tip.
import { useCallback, useEffect, useId, useRef, type ComponentPropsWithRef, type MouseEvent, type ReactNode, type Ref } from 'react';
import { Icon, type IconName } from './Icon';
import './iconbutton.css';

type Props = Omit<ComponentPropsWithRef<'button'>, 'aria-label' | 'children'> & {
  icon: IconName;
  label: string;
  hint?: string;
  children?: ReactNode;
};

const HOVER_DELAY = 300; // ms before a hovered button shows its tip
const GAP = 6; // px between the button and the tip
const EDGE = 4; // px kept between the tip and the viewport edge

const popovers = typeof HTMLElement !== 'undefined' && 'showPopover' in HTMLElement.prototype;

// place puts the tip above the button, centred, inside the viewport.
function place(button: HTMLElement, tip: HTMLElement) {
  const b = button.getBoundingClientRect();
  const w = tip.offsetWidth;
  const h = tip.offsetHeight;
  let y = b.top - h - GAP;
  if (y < EDGE) y = b.bottom + GAP;
  const x = Math.max(EDGE, Math.min(b.left + b.width / 2 - w / 2, window.innerWidth - w - EDGE));
  tip.style.setProperty('--tip-x', `${String(Math.round(x))}px`);
  tip.style.setProperty('--tip-y', `${String(Math.round(y))}px`);
}

function focusVisible(el: Element): boolean {
  try {
    return el.matches(':focus-visible');
  } catch {
    return false;
  }
}

function setRef<T>(ref: Ref<T> | undefined, value: T | null) {
  if (typeof ref === 'function') ref(value);
  else if (ref) ref.current = value;
}

export function IconButton({ icon, label, hint, className, children, type = 'button', ref, onClick, ...rest }: Props) {
  const hintId = useId();
  const button = useRef<HTMLButtonElement | null>(null);
  const tip = useRef<HTMLSpanElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const hide = useCallback(() => {
    clearTimeout(timer.current);
    const el = tip.current;
    if (!el?.dataset.open) return;
    delete el.dataset.open;
    if (popovers && el.matches(':popover-open')) el.hidePopover();
  }, []);

  const show = useCallback(() => {
    const el = tip.current;
    const btn = button.current;
    if (!el || !btn || el.dataset.open || btn.getAttribute('aria-expanded') === 'true') return;
    el.dataset.open = 'true';
    if (popovers) {
      el.showPopover();
      place(btn, el);
    }
  }, []);

  // A scroll moves the button away from a top-layer tip: close it.
  useEffect(() => {
    window.addEventListener('scroll', hide, { capture: true, passive: true });
    return () => {
      window.removeEventListener('scroll', hide, { capture: true });
      clearTimeout(timer.current);
    };
  }, [hide]);

  return (
    <button
      {...rest}
      ref={(el) => {
        button.current = el;
        setRef(ref, el);
      }}
      type={type}
      className={className ? `icon-button ${className}` : 'icon-button'}
      aria-label={label}
      aria-describedby={hint ? hintId : undefined}
      onPointerEnter={(e) => {
        if (e.pointerType !== 'mouse') return;
        clearTimeout(timer.current);
        timer.current = setTimeout(show, HOVER_DELAY);
      }}
      onPointerLeave={hide}
      onFocus={(e) => {
        if (focusVisible(e.currentTarget)) show();
      }}
      onBlur={hide}
      onKeyDown={(e) => {
        if (e.key === 'Escape') hide();
      }}
      onClick={(e: MouseEvent<HTMLButtonElement>) => {
        hide();
        onClick?.(e);
      }}
    >
      <Icon name={icon} />
      {children}
      <span ref={tip} className="icon-button-tip" popover="manual">
        <span aria-hidden="true">{label}</span>
        {hint ? (
          <span id={hintId} className="icon-button-hint">
            {hint}
          </span>
        ) : null}
      </span>
    </button>
  );
}
