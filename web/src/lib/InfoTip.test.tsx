// InfoTip: opens on click (tap) and keyboard, announces through a live region,
// closes on a second press, Escape (focus back on the button) and an outside press.
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { i18n } from '../i18n/setup';
import { InfoTip } from './InfoTip';

function setup() {
  render(
    <div>
      <p>
        Short text
        <InfoTip>Longer explanation</InfoTip>
      </p>
      <button type="button">elsewhere</button>
    </div>,
  );
  return screen.getByRole('button', { name: i18n.t('common:infotip.label') });
}

describe('InfoTip', () => {
  it('toggles on press and announces the text', () => {
    const b = setup();
    expect(b.getAttribute('aria-expanded')).toBe('false');
    expect(screen.queryByText('Longer explanation')).toBeNull();
    fireEvent.click(b);
    expect(b.getAttribute('aria-expanded')).toBe('true');
    const bubble = screen.getByRole('status');
    expect(bubble.textContent).toBe('Longer explanation');
    expect(b.getAttribute('aria-controls')).toBe(bubble.id);
    fireEvent.click(b);
    expect(screen.queryByText('Longer explanation')).toBeNull();
  });

  it('closes on Escape and returns focus', () => {
    const b = setup();
    fireEvent.click(b);
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByText('Longer explanation')).toBeNull();
    expect(document.activeElement).toBe(b);
  });

  it('closes on a press elsewhere', () => {
    const b = setup();
    fireEvent.click(b);
    fireEvent.pointerDown(screen.getByText('elsewhere'));
    expect(b.getAttribute('aria-expanded')).toBe('false');
  });

  it('keeps short text as the paragraph text when closed', () => {
    setup();
    expect(screen.getByText('Short text').textContent).toBe('Short text');
  });
});
