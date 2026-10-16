// API fixtures shared by the post and composer Vitest suites.
export const me = {
  ref: 'r-me',
  handle: 'alice',
  display_name: 'Alice',
  role: 'member',
  inviter: { kind: 'genesis' },
  settings: {
    ui_locale: 'en',
    languages: ['de', 'en'],
    autotranslate: false,
    rank_preset: 'balanced',
    name_in_chains: false,
    suggestible: true,
    named_in_likes: true,
    slippery_default: false,
    ui_prefs: {},
  },
};

export function postJSON(over: Record<string, unknown> = {}) {
  return {
    ref: 'p1',
    author: {
      ref: 'u1',
      handle: 'alice',
      display_name: 'Alice',
      inviter: { kind: 'genesis' },
      chain: { dist: 0, hidden_middle: 0, first_hop_plus: false },
    },
    tier: 0,
    body: 'hello world',
    lang: 'en',
    created_at: '2026-01-01T00:00:00Z',
    audience: 'connected',
    ref_policy: 'anyone',
    can_reference: true,
    mine: true,
    likes: { in_network: 0, others: 0, by_me: false },
    reply_count: 0,
    ...over,
  };
}
