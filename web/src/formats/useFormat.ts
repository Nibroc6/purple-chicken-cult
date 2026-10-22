// useFormat loads a layout format chunk (SCAFFOLD registry), once per session.
import { useQuery } from '@tanstack/react-query';
import { formats } from './registry';
import type { FormatId, FormatModule } from './types';

export function useFormat(id: FormatId, enabled: boolean): FormatModule | undefined {
  const q = useQuery({
    queryKey: ['format', id],
    enabled,
    queryFn: async () => (await formats[id]()).default,
    staleTime: Infinity,
    structuralSharing: false,
  });
  return q.data;
}
