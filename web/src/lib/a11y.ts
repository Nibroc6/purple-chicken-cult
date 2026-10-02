// Accessibility settings are not a format (ARCHITECTURE §15.1): they are switches
// on <html> read by CSS custom properties, applied to every format.
export interface A11yPrefs {
  contrast?: 'normal' | 'high';
  textScale?: 'small' | 'normal' | 'large' | 'xlarge';
  reducedMotion?: boolean;
  dyslexiaFont?: boolean;
}

export function applyA11y(p: A11yPrefs, root: HTMLElement = document.documentElement): void {
  root.dataset.contrast = p.contrast ?? 'normal';
  root.dataset.textScale = p.textScale ?? 'normal';
  root.dataset.reducedMotion = p.reducedMotion ? 'true' : 'false';
  root.dataset.dyslexiaFont = p.dyslexiaFont ? 'true' : 'false';
}
