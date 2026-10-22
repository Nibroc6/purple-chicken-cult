// PostCard: one post as the reader's format draws it in the feed (Cards,
// Compact or Explorer), for pages that list posts outside the feed (Seen,
// Saved, a post's likes). Every action and integrity element sits inside the
// card, and clicking the card opens the post, as in the feed. No seen tracking:
// that belongs to the feed's own tracker. Cards (the default format) is bundled
// here and stands in until another chosen format's chunk loads.
import { useQueryClient } from '@tanstack/react-query';
import { useCallback } from 'react';
import type { PostActions } from '../../formats/types';
import { PostLayout as CardsPostLayout } from '../../formats/cards/PostLayout';
import { useFormat } from '../../formats/useFormat';
import { WhyLoaderContext, type WhyLoader } from '../../integrity/types';
import type { PostVM } from '../../vm/types';
import { fetchWhy } from '../feed/api';
import { useAccountMe } from '../settings/shared';
import { toUIPrefs } from '../settings/vm';

export function PostCard({ post, actions }: { post: PostVM; actions: PostActions }) {
  const qc = useQueryClient();
  const account = useAccountMe();
  const id = toUIPrefs(account.data?.settings.ui_prefs).format;
  const format = useFormat(id, !account.isPending && id !== 'cards');
  const Layout = (id !== 'cards' ? format?.PostLayout : undefined) ?? CardsPostLayout;
  const why = useCallback<WhyLoader>(
    (ref) => qc.query({ queryKey: ['post-card', 'why', ref], queryFn: () => fetchWhy(ref), staleTime: 60_000 }),
    [qc],
  );
  return (
    <WhyLoaderContext.Provider value={why}>
      <Layout post={post} actions={actions} variant="feed" />
    </WhyLoaderContext.Provider>
  );
}
