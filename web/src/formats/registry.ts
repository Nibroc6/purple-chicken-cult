// Format registry (SCAFFOLD): each format is a lazily loaded chunk.
import type { FormatId, FormatModule } from './types';

export const formats: Record<FormatId, () => Promise<{ default: FormatModule }>> = {
  cards: () => import('./cards'),
  compact: () => import('./compact'),
  explorer: () => import('./explorer'), // Phase 1 (P1-W09)
};

export const defaultFormat: FormatId = 'cards';
