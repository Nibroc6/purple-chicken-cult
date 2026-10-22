// The share sheet's search parameter and opener (?share=<post ref>), apart
// from the sheet itself so that other features import no page code.
import { useModalParam } from '../../app/Modal';
import type { Ref } from '../../vm/types';

export const shareParam = 'share';

// useOpenShare opens the share sheet for a post over the current page.
export function useOpenShare(): (post: Ref) => void {
  const [, open] = useModalParam(shareParam);
  return open;
}
