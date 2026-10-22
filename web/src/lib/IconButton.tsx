// An icon-only button with a tooltip: the label is the button's accessible name
// (aria-label) and shows as a small tip on hover and keyboard focus, so sighted
// mouse and keyboard users see the same words a screen reader announces. An
// optional hint adds a second line, also the button's description. Extra
// children (a count) render after the icon. data-part and other attributes pass
// through, so theme CSS can restyle or replace the button (features/themes/sheet.ts).
import { useId, type ComponentPropsWithRef, type ReactNode } from 'react';
import { Icon, type IconName } from './Icon';
import './iconbutton.css';

type Props = Omit<ComponentPropsWithRef<'button'>, 'aria-label' | 'children'> & {
  icon: IconName;
  label: string;
  hint?: string;
  children?: ReactNode;
};

export function IconButton({ icon, label, hint, className, children, type = 'button', ...rest }: Props) {
  const hintId = useId();
  return (
    <button
      {...rest}
      type={type}
      className={className ? `icon-button ${className}` : 'icon-button'}
      aria-label={label}
      aria-describedby={hint ? hintId : undefined}
    >
      <Icon name={icon} />
      {children}
      <span className="icon-button-tip">
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
