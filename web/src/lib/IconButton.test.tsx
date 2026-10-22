// Icon and IconButton: decorative SVGs named by meaning; the button's label is
// its accessible name and its tooltip, and a hint is its description.
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Icon } from './Icon';
import { IconButton } from './IconButton';

describe('Icon', () => {
  it('renders a hidden, themable SVG', () => {
    const { container } = render(<Icon name="like" />);
    const svg = container.querySelector('svg');
    expect(svg?.getAttribute('aria-hidden')).toBe('true');
    expect(svg?.dataset.icon).toBe('like');
    expect(svg?.querySelector('path')?.getAttribute('d')).toBeTruthy();
  });

  it('marks directional icons for mirroring', () => {
    const { container } = render(<Icon name="collapse" />);
    expect(container.querySelector('svg')?.dataset.directional).toBe('true');
  });
});

describe('IconButton', () => {
  it('is named by its label and passes attributes through', () => {
    const click = vi.fn();
    render(<IconButton icon="like" label="Like" data-part="like" aria-pressed={false} onClick={click} />);
    const btn = screen.getByRole('button', { name: 'Like' });
    expect(btn.getAttribute('type')).toBe('button');
    expect(btn.dataset.part).toBe('like');
    expect(btn.querySelector('.icon-button-tip')?.textContent).toBe('Like');
    fireEvent.click(btn);
    expect(click).toHaveBeenCalledOnce();
  });

  it('describes itself with its hint', () => {
    render(<IconButton icon="save" label="Save" hint="Only you can see it." />);
    const btn = screen.getByRole('button', { name: 'Save' });
    const hint = document.getElementById(btn.getAttribute('aria-describedby') ?? '');
    expect(hint?.textContent).toBe('Only you can see it.');
  });
});
