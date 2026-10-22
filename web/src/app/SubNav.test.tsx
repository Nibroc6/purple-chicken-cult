// SubNav marks the current page's tab and links every sibling page.
import { render, screen } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { describe, expect, it } from 'vitest';
import { SubNav } from './SubNav';

describe('SubNav', () => {
  it('links every item and marks only the current one', () => {
    const items = [
      { to: '/friends', label: 'Acquaintances' },
      { to: '/friends/requests', label: 'Requests' },
    ];
    const router = createMemoryRouter([{ path: '*', element: <SubNav label="Chickens" items={items} /> }], { initialEntries: ['/friends/requests'] });
    render(<RouterProvider router={router} />);
    const nav = screen.getByRole('navigation', { name: 'Chickens' });
    const links = [...nav.querySelectorAll('a')];
    expect(links.map((a) => a.getAttribute('href'))).toEqual(['/friends', '/friends/requests']);
    expect(links.map((a) => a.getAttribute('aria-current'))).toEqual([null, 'page']);
  });
});
