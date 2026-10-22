// SubNav (SCAFFOLD): the in-page navigation between sibling pages (the Chickens
// pages, the settings sections, the moderator pages, Saved and Seen). One row
// of tabs with a clear current tab; when the row is wider than the page it
// scrolls sideways instead of wrapping, fades at the edge that has more tabs,
// and starts with the current tab in view.
import { useEffect, useRef, useState } from 'react';
import { NavLink } from 'react-router';
import './subnav.css';

export interface SubNavItem {
  to: string;
  label: string;
  testId?: string;
}

export function SubNav({ label, items }: { label: string; items: SubNavItem[] }) {
  const list = useRef<HTMLUListElement>(null);
  const [more, setMore] = useState({ start: false, end: false });

  useEffect(() => {
    const ul = list.current;
    if (!ul) return;
    const current = ul.querySelector<HTMLElement>('[aria-current="page"]');
    if (current && typeof current.scrollIntoView === 'function') current.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    // scrollLeft runs negative in right-to-left layouts; its size is what counts.
    const measure = () => {
      const at = Math.abs(ul.scrollLeft);
      const room = ul.scrollWidth - ul.clientWidth;
      setMore({ start: at > 1, end: room - at > 1 });
    };
    measure();
    ul.addEventListener('scroll', measure, { passive: true });
    const ro = typeof ResizeObserver === 'function' ? new ResizeObserver(measure) : undefined;
    ro?.observe(ul);
    return () => {
      ul.removeEventListener('scroll', measure);
      ro?.disconnect();
    };
  }, []);

  return (
    <nav className="subnav" aria-label={label} data-more-start={more.start ? 'true' : undefined} data-more-end={more.end ? 'true' : undefined}>
      <ul ref={list}>
        {items.map((i) => (
          <li key={i.to}>
            <NavLink to={i.to} end data-testid={i.testId}>
              {i.label}
            </NavLink>
          </li>
        ))}
      </ul>
    </nav>
  );
}
