# Architecture Sketch (draft v4)

This is the architecture for the ideas in the README. It aims at four things: start as a
single server that's cheap to run, keep the social graph private, be honest about which
parts are hard, and be precise about **what each mechanism actually guarantees**.

**Short version:**
- **One service, not a federation** (§11). Federation can satisfy the requirements
  below, but only with real compromises and months of protocol work. One service
  satisfies them simply.
- **The graph is the defense, not the signup gate.** Bots can exist, but they can't
  reach real people unless real people accept them.
- **The graph is private.** You only learn about connections through your own
  connections. A disconnected part of the network can't learn the rest exists (§2.2).
- **No maximum distance.** Posts reach everyone connected to the author. Each post sits
  in the tier of your **exact shortest distance** to its author, and the chain shown is
  a shortest path (§4).
- **Within a tier, ranking mixes recency with preference from your own network**
  (§5). It never changes who can see a post, only the order.
- **Posts, not comments.** The feed is Instagram-style cards. Any post can reference up
  to 3 parent posts, and opening one shows its **family tree** of connected posts,
  ranked the same way as the feed (§10.1). Short posts are the norm; long ones are
  discouraged but never blocked.
- **Several UI formats** with a clean default, and **custom layouts** that users build
  inside the app from declarative templates. There's **no public API**: it would mostly
  serve scrapers. Scraping and automation are fought with read
  budgets and passkey step-up (§15–§17).
- **Phase 0 is tiny:** one Go binary, Postgres, and a static web app on one cheap VPS.

> Changes from v1–v3 and v4 are summarized in §20.

> **Vocabulary.** The app calls a plain connection an **acquaintance** and an in-person
> verified one (friend+) a **friend**. This document, the code and the signed records
> keep the original level names: `friend` is an acquaintance, `friend+` is a friend, and
> "friends" in the graph sense (1st degree, friend cap, friend requests) means
> acquaintances of either level. Only user-facing text uses the new words.

---

## 1. Big picture

```
 Browser/PWA          ┌──────────────────────────── Service ─────────────────────────────┐
 ┌──────────┐         │  ┌───────────┐   ┌──────────────────────┐   ┌──────────────────┐ │
 │ Web app  │◄──API──►│  │ API       │──►│ gate (visibility)    │──►│ Postgres         │ │
 │ (PWA)    │         │  │ REST+SSE  │   │  ├ graph (in-memory) │   │ signed_records + │ │
 │ WebAuthn │         │  └───────────┘   │  ├ feed · rank       │   │ projections      │ │
 │ camera,  │         │                  │  ├ invites · trust   │   └──────────────────┘ │
 │ mic      │         │                  │  └ moderation        │   (later) Valkey,      │
 └──────────┘         │                  └──────────────────────┘   R2 + media workers   │
      ▲  CDN (signed URLs, later)        └──────────────────────────────────────────────┘
```

- **One Go binary** with modules.
- **Handlers only talk to `gate`**, which enforces the graph horizon (§2.2, §13).
- **The friendship graph lives in memory** as adjacency arrays, loaded from Postgres and
  updated on every edge change.
- **Valkey, object storage and media workers** each come later, only after a measured
  need (§18).

### Suggested stack

| Concern     | Phase 0                                  | Later                                   | Why |
|-------------|------------------------------------------|-----------------------------------------|-----|
| Server      | Go, single binary                        | graph module can split out (§12)        | Cheap, good concurrency |
| Web client  | React + TypeScript (Vite) PWA            | React Native for BLE/UWB                | No public pages → no SSR. The PWA gets camera and mic |
| Primary DB  | Postgres                                 | partitioned; read replicas              | The graph is served from memory, not from the DB |
| Graph       | `[][]uint32` adjacency lists in the binary | CSR arrays; dedicated graph service   | See "Why not a graph database" below |
| Jobs        | Goroutines + a Postgres `jobs` table     | River if needed                         | One less moving part |
| Cache       | none                                     | Valkey (seen sets, rate limits)         | Add when p95 latency demands it |
| Realtime    | SSE                                      | WebSocket only if DMs need it           | Simple |
| Media       | none (text only)                         | R2 + CDN with signed URLs               | Egress is the dominant media cost |
| Hosting     | One €5–20/mo VPS, Cloudflare in front    | bigger box, then replicas (§12)         | "Economical" |
| Telemetry   | Self-hosted or ID-free (§2.3)            | same                                    | Graph data never goes to a third party |

**Why not a graph database.** The feed doesn't run point-to-point shortest-path queries.
It runs **one breadth-first search (BFS) from the reader**, then does one array lookup
per candidate post. A graph DB would mean one `shortestPath` call per candidate, or
streaming millions of distance rows over a query protocol. Either is slower than an
in-memory BFS, and both add a stateful system to run and keep in sync. A graph DB (or
the Apache AGE extension for Postgres) may still be worth it later for **offline**
moderation and analysis queries.

---

## 2. Trust & privacy model (read this first)

### 2.1 Who has to be honest

| Guarantee | Holds against | Does **not** hold against |
|-----------|---------------|---------------------------|
| A bot with no accepted edge to a real person reaches no one | Everyone | A real person accepting the bot (by design; they're now in the blame chain) |
| An edge needs both users' consent | Other users; the API | The operator or an insider with database access. **Passkey co-signing** (§3) makes forgery detectable and costly; device keys (Phase 3) make it infeasible |
| Graph privacy (§2.2) | Every user, including disconnected components | The operator, which must hold the graph to compute distances. Limited by insider controls (§2.3) |
| An author's audience limit | Every user | The operator |
| Proximity ("met in person") | Remote mass invites | One person holding many phones (§7) |
| Scraping | Bulk scraping: nothing is public, there's no public API, read budgets apply per account, automation signals trigger a passkey step-up, and sessions are device-bound where the browser supports it (§17) | A logged-in person recording their own view at human pace. They get only their own horizon. Pooling horizons needs many real, invited accounts, each traceable through invite blame |
| One person, one action | Unattended scripts: human-pace write limits, behavioral signals, and passkey step-up (§17) | A person who personally drives automation within human limits. This is a deterrent, not a proof |

### 2.2 The graph horizon (hard requirement)

> **The connection graph is visible only through connections.**

For a user V, the **horizon H(V)** is the set of graph facts V is entitled to:
1. V's own edges, pending requests, and invite relations (V's inviter and invitees).
2. For each user U whose content V can currently see: that U exists, `dist(V, U)`, and
   the **first hop** of the chain shown. So a chain `you → f → U` reveals that f and U
   are friends. That's accepted; it's the minimum needed for chains.
3. Middle hops of a chain, only where **both endpoints of every named edge** opted in to
   being named.
4. **U's inviter label** (§3): the inviter's handle, shown as a non-interactive tag, even
   if the inviter is otherwise outside H(V), for example after an unfriend. This is
   an accepted disclosure, because invite blame is a core feature.
5. People-you-may-know candidates (§10.5), with a mutual-friend *count* only.
6. **Aggregate engagement counts** on posts V can see, from people within 2 hops of V.
   These are never lists, and they never include anyone farther away. They are the only
   information V gets about people outside (2).
7. Links between posts (parent ↔ child) only when both posts are visible to V **and** the
   edge-visibility rule in §10.1.3 holds. In particular, on a post with an audience
   limit or a non-default `ref_policy`, V sees only children written by V's own friends.

**Every output to V must be a function only of H(V), content V can see, and V's own
actions.** That covers bodies, status codes, errors, counts, orderings, notifications and
latency outside a fixed bucket. Users in different connected components are mutually
invisible: nothing reveals that the other component exists.

This is enforced structurally, by the `gate` module and opaque references (§13), and
tested as **noninterference properties**: change anything outside H(V), replay V's API
calls, and the responses must be identical.

### 2.3 The operator and insiders
The service has to hold the full graph, so the threat is insiders, breaches and
subpoenas:
- **Encryption at rest** for the database, backups and logs.
- **No staff access to graph data**, except through a logged, two-person-approved
  break-glass procedure.
- **No user IDs, edges, chains or IPs in telemetry.** Hash identifiers with a rotating
  salt, or self-host the telemetry.
- **Retention:** revoked-edge events are purged after 30 days, and backups expire on the
  same schedule. Deletion stubs (§10.2) are visible to moderators only.
- **Transparency log** (Phase 2): an append-only Merkle log of signed-record hashes.
  Clients can check that their own edges and invites haven't been altered or invented.
- **A transparency report** on government requests.

---

## 3. Identity, keys & data model

### Identity
- **Account ID:** an opaque, stable UUIDv7. It never appears in the API (§13 uses
  per-viewer references instead).
- **Handle:** `@alice`. **Handles do not resolve publicly.** No API takes a raw handle as
  input. Mentions resolve only within the author's horizon (§10.6).

### Signed records (defense against insiders)
- **Phase 0:** one server signing key, with key IDs and validity windows published in
  exports.
- **Phase 1+:** per-user custodial Ed25519 keys. **High-value records also carry a
  passkey (WebAuthn) assertion:** `Invite`, `Friendship`, `Revocation` and passkey
  changes. The challenge is
  `H("pcc-v1:" ‖ kind ‖ JCS(record minus signatures/assertions))`. An insider who forges
  an edge can't produce the assertion, so the forgery shows up in audits and the
  transparency log.
  - **Limit:** the server serves the web app, so a malicious *deployment* could trick an
    active user into signing. Device keys plus a client that can be checked
    independently (native app, Phase 3) close that gap.
- Store the **exact signed bytes**, canonicalized with JCS (RFC 8785), with a
  `schema_version` on every record.

```
Invite      { inviter, invitee, issued_at, handshake, sig_inviter, sig_invitee, passkey_assertions }
Friendship  { a, b, level: "friend" | "friend+", created_at, handshake?, sigs, passkey_assertions }
Revocation  { edge_id, by, at, sig, passkey_assertion }
Post        { id, version, author, body, lang, media: [...], audience?, parents: [{id, kind}],
              ref_policy?, no_preview?, created_at, sig }      -- kind: "ref" | "cont" (§10.1)
                                                              -- each version signed separately
Tombstone   { target, by, at, sig }
Block       { blocker, blocked, at }        -- never shown to the blocked person
Report      { reporter, target, reason, at }
```

**Invites and "invited by":**
- **An invite creates two records:** the permanent `Invite` and a `Friendship`. From
  Phase 1 that friendship starts as friend+, since an in-person handshake is exactly
  what friend+ means.
- **The same code connects existing accounts** (§7 "One code, two uses"): a signed-in
  scan creates only a co-signed `Friendship`, no `Invite`.
- **Unfriending revokes the `Friendship`, never the `Invite`.** Unfriending is like
  blocking, except the person isn't removed from your indirect paths.
- **Everyone who can see a user sees that user's inviter label** ("invited by @bob"),
  including after an unfriend, a split into separate components, or any other way the
  inviter falls outside the viewer's horizon.
- **The label is display-only.** It has no per-viewer ref, no profile link, no content,
  no friend request and no message. The one action it allows is **"report inviter"**:
  it goes to moderators, routed through the *invitee's* ref, so the inviter is never
  addressable.
- **Special labels:** a deleted inviter shows as "a deleted account", and genesis users
  show "genesis".

### Postgres

```sql
signed_records(hash bytea PRIMARY KEY, kind smallint, author_id bigint,
               body bytea, sig bytea, schema_version smallint, created_at timestamptz)

users(id bigint PRIMARY KEY, ext_id uuid UNIQUE, handle text UNIQUE, invited_by bigint,
      created_at timestamptz, status smallint, probation_until timestamptz,
      name_in_chains bool, suggestible bool, named_in_likes bool,
      ui_prefs jsonb /* {format, theme, density, a11y, layout_template} */,
      ui_locale text, languages text[], translate_to text, autotranslate bool)
edges(user_id bigint, friend_id bigint, level smallint, since timestamptz,
      PRIMARY KEY (user_id, friend_id))                              -- both directions
edge_events(id uuid, a bigint, b bigint, kind text, at timestamptz, record_hash bytea)
invites(id uuid, inviter_id bigint, invitee_id bigint UNIQUE, created_at timestamptz, record_hash bytea)
posts(id uuid PRIMARY KEY, author_id bigint, body text,
      audience smallint /* NULL = everyone connected */, ref_policy smallint, lang text,
      no_preview bool, version int, created_at timestamptz, edited_at timestamptz,
      deleted_at timestamptz)
  -- INDEX (author_id, created_at DESC)
post_parents(child_id uuid, parent_id uuid, ord smallint, kind smallint /* 0 ref, 1 cont */,
      child_author bigint, detached bool, shadow bool, created_at timestamptz,
      PRIMARY KEY (child_id, parent_id))
  -- INDEX (parent_id, created_at DESC) INCLUDE (child_id, child_author, kind)
  -- UNIQUE (parent_id) WHERE kind = 1       -- at most one continuation per post
link_previews(post_id uuid, ord smallint, url_canon text, title text, description text,
      site_host text, status smallint, fetched_at timestamptz,
      PRIMARY KEY (post_id, ord))           -- per-post copy; the fetch cache is internal (§10.1.6)
post_translations(post_id uuid, version int, target_lang text, body text,
      created_at timestamptz, PRIMARY KEY (post_id, version, target_lang))  -- Phase 2+
layout_templates(id uuid, owner_id bigint, name text, body jsonb, created_at timestamptz)  -- §16.2
seen(user_id bigint, post_id uuid, at timestamptz, PRIMARY KEY (user_id, post_id))
engagements(user_id bigint, post_id uuid, kind smallint, at timestamptz,
            PRIMARY KEY (user_id, post_id, kind))
blocks(blocker bigint, blocked bigint)
jobs(...)
```

- **Friend cap: 1,000 edges.** Real-world degree is heavy-tailed. The cap bounds every
  per-user cost and fits the "real connections" stance.

---

## 4. Feed: shortest-distance tiers, unlimited reach

### Rules
1. **You see posts from everyone you have a path to, at any distance.** You never see
   posts from people you have no path to.
2. **A post's tier is your exact shortest-path distance to its author.** That distance is
   computed over paths that avoid anyone you've blocked or hidden-via. **The chain shown
   is always a shortest path.** You never see `A → B → C → D` when A and D are friends.
3. Tiers are strict: all **unseen** 1st-degree posts come first, then unseen 2nd-degree,
   and so on. Posts within a tier are **ranked** (§5).
4. **An author may optionally limit a post's audience** to 1st, 2nd or 3rd degree. The
   default is "everyone connected", and the system imposes **no maximum distance**. A
   limit is checked against the reader's exact distance and fails closed.
5. **New accounts on probation** (§8) are the one exception: their posts reach 1st degree
   only, until probation ends.

### 4.1 Exact distance: lazy BFS from the reader
- **The search.** Per reader, the feed module runs a **layered BFS** that skips blocked
  and hidden-via users. Each layer `L_k` is the exact set of users at distance k.
- **Chain labels.** The BFS carries a `firstHop` label for each visited user. When a user
  can be reached from several predecessors in the previous layer, it keeps the
  predecessor whose first hop ranks best by **the reader's own edge attributes**:
  friend+ first (from Phase 1), then the oldest edge, then ID. No global data such as
  trust or other people's activity influences the choice. Every chain is a shortest path
  by construction.
- **Laziness.** Layer k+1 is expanded only once the reader has finished tier k.

| Layer | Work at 1M users | Time | Storage |
|------:|------------------|-----:|---------|
| 1–2 | ~150 + ~20k visits | < 1 ms | sparse map |
| 3 | ~3M edge visits | ~20–50 ms | sparse map |
| 4+ | rest of the component, ~150M edge visits | ~200–500 ms | dense `uint16 dist[]` + `firstHop[]` while running (~6 MB), then reduced to entries for **authors with posts in the window** |

- **Fixed-latency delivery.** Tiers 4 and deeper are computed asynchronously and released
  at a fixed latency bucket, so the response time can't reveal how big the component is
  (§2.2).
- **Caching.** A reader's search result is kept for up to 30 minutes, but **any edge
  removal or block anywhere bumps a global epoch**, and that drops every cached result.
  Removals are rare. Additions don't invalidate caches, because a missed new path only
  delays content; it never exposes anything.
- **Reuse.** At most one full deep search per reader per day. Later deep tiers reuse it,
  subject to the epoch rule.
- **Phase 0:** at ≤5k users a full BFS takes under 1 ms, so just run it on every feed
  open. No laziness and no caching.

### 4.2 Candidate posts per tier (one algorithm for all tiers)
- **Index.** Keep `recent[author]` in memory: post references inside the 7-day window,
  newest first. Entries are appended on post, dropped on delete, and trimmed nightly.
  That's ~7M references (~170 MB) at 1M users.
- **Cost of each route.** For tier k, with BFS layer `L_k`, compute
  `P_k = Σ_{a ∈ L_k} len(recent[a])` exactly. Then choose the cheaper of:
  - the **author scan**, a merge of `recent[a]` for every a in `L_k`, costing `|L_k| + P_k`;
  - the **time scan**, which walks all W posts in the window and keeps those whose author
    is at distance k, costing `W`.
- **Which route wins.** Tiers 1 and 2, and sparse deep tiers, use the author scan. The
  time scan only wins when tier k covers most of the network.
- **Ranking.** **Every candidate in the tier is ranked** (§5.6). There's never an early
  stop.
- **Feed end.** When every tier is exhausted, the feed shows "You're caught up."
- **Small networks:** below ~10k users nearly everyone is within 2–3 hops. Design the UX
  for about 3 tiers.

### 4.3 Seen state and pagination
- **Seen** means ~1 s at ≥50% visible. Seen state is kept for 14 days. A "recently seen"
  view recovers posts you scrolled past quickly.
- **Snapshots.** Each tier is ranked and snapshotted **when the reader first reaches
  it**. The cursor is `(session, tier, offset)`, so pages don't reshuffle.
- **New arrivals.** New posts in a closer tier show a "N new posts above" banner.

### 4.4 Bot isolation and blame
- A bot with no accepted edges has no path, so it reaches no one.
- With unlimited reach, one person who friends a bot connects it to their whole
  component. So the defenses are:
  - **Probation:** a new account reaches 1st degree only (§8).
  - **Collapse at degree ≥3** for low-trust authors ("3 posts from distant, low-trust
    connections"). In Phases 0–1 this applies to accounts younger than ~60 days; later it
    uses trust scores (§8).
  - **The chain is the blame.** You see your first hop and can unfriend, hide-via or
    report them. They can see *their* next hop.

### 4.5 Chain display
- **Always shown:** your friend (the first hop) and the author.
- **The middle collapses** ("you → Bob → 2 others → author") unless every named edge has
  both endpoints opted in (§2.2).
- **Verification:** the server checks chains internally from signed edges.

### 4.6 Unfriending and blocking
Any removal bumps the graph epoch (§4.1), so the change takes effect on the next feed
load for everyone.

---

## 5. Ranking within a tier

**Invariant:** ranking only **reorders** a tier's candidates. It never adds or removes a
post, never moves one across tiers, and never extends the window. A property test checks
that the ranked feed is a permutation of the unranked one.

### 5.1 Formula: a bounded time shift
Each post gets an **effective timestamp**. Within a tier, posts sort by it, newest first:

```
t_eff = created_at + clamp( λ · τ · ln( (1 + n_eff) / (1 + b) ), −S, +S )
                   − λ · R · is_reply

n        = network preference for this post, as seen from the reader (§5.2)
n_eff    = n + min( β · Σ n_kid(C) , n )                          -- upward credit
n_kid(C) = n of child C + β · Σ n of C's children                 -- 2 generations
           summed over at most the K = 20 newest children per post whose `ref` links
           are visible to V (§10.1.3). Links that are cont, detached or shadow, and
           self-replies, never count.
b        = the same author's median n over their previous 10 posts, excluding this one,
           from the same reader; b = 0 if the author has fewer than 3 previous posts
is_reply = 1 if the post (for a thread: its head part) has a `ref` parent; else 0
τ = 24 h     S = λ · τ · ln 4     β = 0.3     R = 12 h
```

**Replies are ranked, not filtered.** Replies appear in the feed like any other post, at
their own author's distance. Two terms shape the ranking:
- **Reply penalty.** A reply starts `λ·R` behind: 6 h under Balanced, 12 h under Top. It
  always applies, outside the clamp. That keeps random replies from diluting the feed at
  *any* distance, including from friends.
- **PageRank-like upward credit.** A post gains from the network preference of its
  **replies**, and more weakly from their replies (β = 0.3, then β²). A post that starts a
  conversation your network likes rises, in the feed and in the family-tree rows.
  - **The credit is capped at the post's own `n`.** A post nobody in your network liked
    gains nothing, however well-liked the replies dunking on it are. A liked post can
    at most double its preference through its replies.
  - **Only reply quality counts, never quantity.** A pile of replies nobody likes adds
    nothing, so outrage threads don't rise. This replaces the old per-reply credit.
  - **Authors stay in control.** Detaching a reply, or making the post slippery, removes
    that reply's credit.
- **Only visible links count.** Children V can't see contribute nothing, so ranking
  can't leak them. Each child's `n` is itself built only from the reader's 2-hop network.
- **Latest stays chronological.** Both terms scale with λ, so they vanish when λ = 0.
- **Seen posts don't come back.** Upward credit arrives as replies come in, so it helps
  posts you haven't seen yet. A seen post never comes back because its replies got
  popular.

| Preset | λ | Preference shift | Reply penalty | Meaning |
|---|---|---|---|---|
| Latest | 0 | 0 | 0 | Pure chronological order |
| **Balanced (default)** | 0.5 | ±17 h | −6 h | Recency first, nudged by your network |
| Top (Phase 1) | 1 | ±33 h | −12 h | Preference matters more |

**Balanced in plain language:** a post moves up about **8 hours each time the number of
people in your network who liked it doubles**, up to a maximum of 17 hours. Replies start
6 hours behind. Latest moves nothing.

- **Ties** go to friend+ first hop, then ID. Trust plays no part, because it's global data.
- **Cold start:** for a post younger than 2 h, the clamp term is floored at 0. The reply
  penalty still applies.
- **Author cap:** at most 2 posts from the same author in any 10 consecutive slots. It's
  **off under Latest**, which stays exactly chronological.
- **Seen posts never come back** because they gained engagement.

### 5.2 Network preference
**Preference is collective, but only within your own network.** It counts engagement
from people **within 2 hops of the reader**, which is exactly the aggregate H(V) allows
(§2.2(6)). This keeps three things in check:
- **Collusion rings** only influence people who friended their members.
- **Ordering** can't leak anything from outside the horizon.
- **Communities with different norms** don't rank each other's feeds.

| Signal | Weight | Notes |
|---|---|---|
| Like | +1 | Toggle |
| Save | +2 | Private. Counts in the score anonymously and is never displayed (Phase 1) |


Replies don't count as engagement on their own. A post gains from its replies only
through the **upward credit** in §5.1, which measures how much your network liked them,
not how many there are. Nothing flows downward: a reply gains nothing from its parent.

```
n = Σ over engagers u within 2 hops of the reader:
      weight(u) × proximity(u) × min(4, Σ of u's signals)
proximity(u) = 1.0 for the reader's friends, 0.5 for 2nd degree
```

**Engager weight:**
- **Probation accounts and the author** count for 0.
- **One inviter's invitees** together contribute at most 4 units per post.
- **From Phase 2,** a weight of `min(1, trust / median)`.

**Personal signals.** These affect only your own feed:
- **Hide post or "show less":** removes the post and lowers that author *for you*.
- **Mute author:** filters the author out of your feed.
- **Report:** goes to moderation only. It is **never** a ranking signal.

**Deliberately not collected for ranking** (a deviation from "all forms of engagement we
can easily collect"):

| Signal | Why |
|---|---|
| Dwell time, expand / "read more", media plays | They reward clickbait and length, are easy to fake with scripts, and amount to reading surveillance |
| Raw reply counts | The classic outrage amplifier |
| Collective hides and reports | They would let a group bury people without review |

### 5.3 What people see
- **Like counts:** only likes from within 2 hops of the viewer, shown as "14 in your
  network". This applies to the author too.
- **Names:** only the viewer's own 1st-degree friends ("Liked by Bob, Carol and 12
  others"), and anyone can opt out of being named.
- **Never shown:** saves, impressions, hides, scores and trust.
- **"Why am I seeing this?"** appears on every post, for example:
  > **1st degree, via Bob.** Posted 9 h ago, moved up ~4 h because people in your network
  > liked it more than this author's usual posts. Setting: Balanced. [How ranking works →]

  Reason codes (§15.4) include `network_likes`, `below_author_usual`, `reply_penalty`,
  `good_replies` (people in your network liked its replies) and `cold_start`.

### 5.4 Cost
- About 10⁷ engagement rows a day at 1M users.
- Scoring a tier costs one distance lookup per engager, plus a roaring bitmap
  intersection for hot posts.
- Impressions aren't counted for ranking.
- **Upward credit** reads at most K = 20 children and K² = 400 grandchildren per post. Each
  costs one bitmap intersection of that post's likers with the reader's 2-hop set,
  memoized per post within a snapshot. Child links come from the `post_parents` index.

### 5.5 Phase 0 subset
- `n` is the number of distinct people within 2 hops who liked the post, weighted 1 for
  friends and 0.5 for 2nd degree.
- `b = 0`.
- Reply penalty, plus upward credit from **direct** replies only (no grandchildren yet).
- No trust weighting.
- The presets are Latest and Balanced.
- Every `t_eff` component is logged for comparison against chronological order.

### 5.6 Snapshot completeness
A tier snapshot ranks **every** candidate the §4.2 scan produced for that tier. Never
rank a prefix of the tier: a post up to S older could belong above the last one kept.

---

## 6. Media (images & video): Phase 1+

1. **Upload pipeline.** CSAM hash-matching (PhotoDNA, which requires an application, or
   Cloudflare's CSAM scanning tool) runs **before** storage. US operators must report to
   NCMEC and preserve flagged material as the law requires. Apply for PhotoDNA access
   **before** Phase 1.
2. **Transcode once.** Images become AVIF/WebP in a few sizes. Video starts with a single
   720p HLS rendition.
3. **Storage.** R2 behind a CDN, with **short-lived signed URLs** issued after the gate
   checks access. Blob keys are random, never content hashes.
4. **Fetch on view.** Media is only fetched when someone scrolls to it, so attention
   bounds the cost.

---

## 7. In-person proof (invites & friend+)

### What it proves
"**A device near this device completed the handshake.**" It doesn't prove one human per
account. Its job is to make *remote, mass* invites expensive and to attach a real account
to each invite. The invite budget (§8) limits how fast one person can grow a subtree.

### Handshake (Phase 1)

```
  Inviter (logged in, display)               Invitee (camera + mic)
  ────────────────────────────               ──────────────────────
  1. Server opens handshake session H.
     Screen shows ANIMATED code, rotating
     ~250ms: {H, E_i (ephemeral pubkey),
     nonce_t}
                                   ──scan──► 2. Generates ephemeral E_v and sends it to the
                                                server under H. Computes S = ECDH(e_v, E_i)
  3. Server relays E_v to the inviter's
     device, which computes S
                                   ◄─sound── 4. Plays ONE ~1.5 s sound with two layers at
                                                once (see "The handshake sound"):
                                                • near-ultrasonic: MAC_S(transcript)[:32]
                                                • audible jingle: brand motif +
                                                  MAC_S(transcript ‖ "jingle")[:24]
                                                transcript = H‖E_i‖E_v‖nonce_t‖inviter_id
  5. Inviter's mic decodes whichever layers
     it hears. Each one must match the frame
     shown within a FIXED upper time bound.
     The decoded results go into the
     handshake score
  6. If the score passes, the server issues a single-use signup token BOUND TO E_v.
     A photographed QR is useless without e_v. Both sides sign the Invite and the
     Friendship (§3) with passkey assertions.
```

- **No self-reported latency calibration.** Calibration can only *tighten* the window.
- **The audio carries MAC bits**, never keys. It's decoded **on the device**. No audio is
  recorded, uploaded or stored; only the decoded bits and timings are sent.
- **The QR doesn't reveal the inviter's identity** until the token is bound. Every
  redemption failure returns the same error.
- **Phase 0:** a QR code that rotates every ~10 s, plus a token bound to the invitee's
  session. No audio yet.

### The handshake sound
Both layers play **at the same time**, in separate frequency bands, so the handshake
takes no longer than either one alone.

- **The jingle.** This is the audible layer (roughly 400–4,000 Hz), and it doubles as
  the app's **sound logo**.
  - **Fixed motif:** the jingle opens with the same ~3-note motif every time.
  - **Unique tail:** ~8 notes follow, chosen from one pleasant 8-tone scale over a fixed
    rhythm template. That's 3 bits per note, carrying a 24-bit MAC. Every handshake has
    its own tune, but they all sound like the same "song family".
  - **Decoding:** Goertzel tone detection.
  - **Sound design** is a real Phase 1 task: it has to stay pleasant across thousands of
    variations and remain decodable through cheap phone speakers.
- **Volume reminder.** If the inviter's device hears the motif faintly or not at all, the
  invitee's screen shows **"turn your volume up"** and the handshake retries.
- **Optional jingle.** A per-handshake "quiet mode" plays only the ultrasonic layer. If
  ultrasonic fails on that device pair, the app suggests switching the jingle back on.
- **The ultrasonic layer** (18–20 kHz, frequency-shift keying) is mostly inaudible,
  short-range, and **filtered out by most calling apps**. That's why it counts most
  toward the score. Some devices produce or hear it poorly, which is why the jingle
  exists as a second channel.
- ~~**Near-infrasound**~~ won't work. Speakers can't produce it, mics filter it, and it
  goes through walls.

### Handshake score
Each channel adds evidence. The server combines it into a score and turns that into
**one of three outcomes**. Users never see a number, only "Verified in person" or a
retry prompt.

| Evidence | Weight | Why |
|---|---|---|
| A fresh QR frame was scanned (the camera sees the screen within ~1 s) | base | Always present. A screen can be relayed over a video call |
| The jingle MAC is decoded within the time bound | medium | Audible audio survives calls, so only the timing limits relays |
| The ultrasonic MAC is decoded within the time bound | high | Calling apps usually strip it, so a relay must be custom-built |
| Both layers arrive in sync (offset ≈ 0) | bonus | A relay that passes one band but not the other gives itself away |
| Later: BLE RSSI, or UWB ranging (native app) | highest | UWB gives real distance bounding |

| Outcome | Requires | Result |
|---|---|---|
| **Verified in person** | Ultrasonic (or UWB) **plus** QR | Invite valid. The friendship starts as **friend+** |
| **Accepted** | QR plus jingle | Invite valid. The friendship starts as plain friend, and a later verified handshake upgrades it |
| **Retry** | Anything less | No token. The app shows the volume prompt or the quiet-mode suggestion |

- **One code, two uses.** The inviter's screen shows a single code. A guest session that
  scans it signs up (an invite: needs the inviter's budget, checked when the guest
  claims). A **signed-in** account that scans it turns the handshake into a *connect*:
  - strangers: Verified makes friend+, Accepted makes friend; both sides co-sign the
    `Friendship` with a passkey, and no invite is spent (so it works on probation too);
  - plain friends: it becomes a friend+ upgrade (Verified only);
  - friend+ already: `already_friends`; a block either way, an inactive account or a
    full friend list: the uniform not-found.

  This is the in-person route of §10.5, and how new accounts earn the friend+ edges
  outside their subtree that end probation (§8).
- **Stored results.** The `Invite` and `Friendship` records keep which channels passed
  and the timings, bucketed, in the `handshake` field. That's all.
- **How the score is used:**
  - Verified friend+ edges count toward leaving probation (§8).
  - They're preferred trust seeds (§8).
  - A "friend+ only" audience requires them.
- **Accessibility fallback.** This covers deaf users and devices without working audio.
  Each phone shows a QR, and each scans the other's within a tight window. The result
  can be **Accepted**, but never Verified, until a later audio or UWB handshake.

The timing bound isn't a hard security boundary: a custom low-latency relay that carries
ultrasonic audio can beat it. That's acceptable, because a relay still needs a
cooperating account with invite budget on the other end. Web Bluetooth and Web NFC don't
work on iOS Safari, so a browser can only use the camera and microphone. A native app
(Phase 3) adds **UWB**.

---

## 8. Sybil resistance: invites, probation, trust

The invite tree grows **exponentially** if every account can invite. One person with
many phones could build 2ⁿ accounts in n days at "1 invite per 24h". So:

### Invite budget
- **Probation:** no invites for 30 days. Probation lasts until the account also has ≥3
  friend+ edges to people **outside its own invite subtree**, whichever comes later.
  Those edges come from in-person connects (§7): the invite code still opens on
  probation, it just can't sign anyone up.
  - *Phase 0:* 7 days, plus ≥3 ordinary edges outside the subtree.
- **After probation:** a token bucket of +1 invite per day, capped at 3. Genesis users get
  a cap of 5.
- **Visible budget:** the invite screen shows how many invites you have saved up. If you
  have none, it shows a **countdown to the next one**. During probation it shows a
  countdown to the end of the time-based part ("Invites unlock in 4 days"), plus what's
  still needed ("1 more friend outside your invite tree"). The budget is visible only to
  its owner.
- **Diversity decay (Phase 1):** accounts whose edges mostly stay inside their inviter's
  subtree earn invites more slowly.

### Probation
- Posts reach 1st degree only.
- No invites.
- A small daily cap on new friend requests.
- Engagement weight is 0.
- **Friends get alerted** (in-app, from Phase 0) about passkey changes and recovery
  events.
- **Probation status is visible only to moderators and the inviter.**

### Trust scores (Phase 2+)
- **SybilRank-style** propagation from many diverse seed users, computed nightly inside
  the service. **Each connected component is computed with its own seeds**, so a
  component's scores never depend on whether other components exist.
- **Edge weights:** friend+ 1.0, friend 0.5, edges younger than 14 days 0.2.
- **Scores are never shown.** They're used only for collapse at degree ≥3 and for
  engagement weight.

### Invite-tree quarantine
If ≥k accounts in a subtree are **confirmed** bad by moderators:
- The subtree goes back to probation.
- The direct inviter's invites pause for 30 days.
- A second offense revokes invite privileges.

An "invited a removed account" mark lasts 90 days. It is visible only to moderators and
to users who had the removed account in their horizon when it was removed.

Nothing propagates up the tree automatically. **No blame mark and no inviter
notification happen without moderator confirmation.**

### Metrics
- The number of edges between flagged clusters and everyone else.
- The share of degree ≤3 impressions that come from flagged authors (target ≈ 0).
- New-account trust, grouped by inviter.

---

## 9. Captcha / "human check"

**ARC-AGI-style puzzles won't work as a gate:**
- Frontier AI has largely beaten ARC-AGI-1.
- Many humans fail them.
- A fixed puzzle set leaks.
- Captcha farms are cheap.
- The bigger threat is real humans running AI agents on their own accounts.

Browser-level bot detection loses to residential-proxy botnets running real browsers.

**Leave a botnet nothing to hit:**
1. **No account without an in-person invite.** Signup redeems a token bound to the
   handshake (§7).
2. **No public content and no public lookups.** Nothing to scrape and no oracle to query.
3. **A tiny unauthenticated surface** (landing page, invite redemption, passkey login)
   with strict rate limits and WAF rules.
4. **Strict per-account limits** on posting, friend requests and invites.
5. **Behavioral signals** (posting cadence, similarity across accounts, invite-tree
   clustering) feed a human review queue.

A puzzle can still serve as an optional speed bump for flagged accounts.

---

## 10. Product mechanics

### 10.1 Posts, references & the family-tree view

There are **no comments**. Every response is a post, and posts have no titles. A post may
reference **up to 3 earlier posts as parents**. Parents and children form a DAG: the
post's "family tree".

#### 10.1.1 Model & rules
- **Two kinds of link.** `ref` is an ordinary reference to anyone's post. `cont` is a
  continuation of **your own** post, and it's how threads work (§10.1.4). A post has at
  most one continuation.
- **Parents are fixed at creation and must already exist,** so cycles are impossible.
  An edit changes the body and media only. It keeps the same `id` and bumps `version`,
  so children keep pointing at it and readers see the latest version marked "edited".
  A reference creates a new post; an edit does not.
- **You can only reference what you can see.** Parents arrive as per-viewer refs (§13),
  and the gate accepts one only if the author can currently see that post. A bad ref
  gets the uniform not-found response, so referencing can't be used to probe for posts.
- **No reach effects.** A child is an ordinary post. Its visibility depends only on its
  own author and audience limit. Referencing a popular or distant post never widens
  who sees the child, and neither post is added to the other's audience.- **Anti-pile-on controls.** These handle "dunking", where people reference someone's
  post to pile on:
  - **Who may reference:** each post sets `ref_policy` to anyone who can see it (the
    default), people within 2 hops, friends only, or **nobody ("slippery")**.
  - **Slippery posts** can't be referenced by anyone else. The reply action is replaced
    by a small "replies off" mark, and only the author can still continue the post as a
    thread (`cont`).
    - **Changing it later:** the policy can be changed at any time. Turning slippery on
      blocks new references, and the author is offered "detach all" for existing ones.
    - **Default for new posts:** users can make slippery the default in settings.
  - **Blocks:** a reference between two people where either has blocked the other is
    **accepted but stored as a shadow link**. Only the child's author sees the link, so
    the block is never revealed. The parent's author is never notified, and the link
    gives the parent no upward credit.
  - **Daily cap:** an author can reference the same post at most 2 times per day.
  - **Detach:** a parent's author can **detach** any child. The link then disappears
    everywhere, but the child post stays.
  - **No borrowed ranking:** a reply gains nothing from a popular parent, and replies
    carry a ranking penalty. Credit flows only upward, from well-liked replies to their
    parent. It's capped at the parent's own preference, so a dunked-on post that nobody
    liked never rises because of the dunks (§5.1).
- **Notifications.** The parent's author is notified only if they can see the child.
  Notifications are batched per hour ("5 new posts reference yours"). By default only
  children from people within 2 hops trigger them, and they can be muted per post.

#### 10.1.2 In the feed
- **A child appears in the feed** in the tier of *its own author's* distance. It carries
  a one-line context row ("↳ replying to @a: first 80 chars…"), shown only if the link
  is visible to the reader (§10.1.3).
- **Seeing that context row never marks the parent as seen.** Otherwise "unseen first"
  would break.
- **There's no reply filter.** Every reply appears in the feed at its author's distance.
  Ranking handles dilution: replies carry a penalty, while posts whose replies your
  network likes get upward credit (§5.1).
- **A continuation thread is one feed item**, a stacked card ("1/3, 2/3…"). Continuations
  aren't replies, so they get no reply penalty.
  - **How the card is ranked:** it's ranked and seen-tracked as its **newest unseen
    visible part**. Earlier parts are shown collapsed as context and aren't candidates
    themselves. A card whose parts are all seen doesn't come back.

#### 10.1.3 Horizon rules for links
A link C→P is shown to viewer V iff **all** of these hold:
- V can see C, and V can see P.
- The link isn't detached.
- C isn't a shadow link (see blocks, §10.1.1), unless V is C's author.
- P has no audience limit (probation counts as a limit) **and** uses the default
  `ref_policy`, **or** C's author is V, P's author, or one of V's friends.

The last condition matters because a child on a limited post, or on a post restricted
to friends or 2 hops, reveals that its author falls inside that limit.

- **Fails closed.** When the condition fails, nothing is shown: no placeholder, no
  "hidden post", no counts, no "load earlier" affordance.
- **Visible links only.** Rows, "+N" chips and reply counts are built only from links
  V can see.
- **Traversal stops at hidden posts.** The tree never routes through a post V can't
  see.
- **A visible child whose parents are all hidden** looks exactly like a root post.
  Deleted parents get the same treatment, so deletion and invisibility can't be told
  apart.
- **The composer warns** when a parent has an audience limit: "Some people who see your
  post won't see what it replies to."

#### 10.1.4 The family-tree view
Tapping a post opens it as the **focal post**. Each generation is one horizontal **row**,
and rows stack vertically:

```
  ── Earlier ──            ↑ load earlier (parents of the centered parent)
  ── Replying to ──        [P1]  P2 ›                parents of the focal post
  ── This post ──     ‹ S2  [ FOCAL ]  S3 ›          siblings under the primary parent
                                                    (= highest-ranked visible parent;
                                                     no sibling row if there is none)
  ── Replies ──            [C1]  C2 › C3 › +12       children of the centered post above
  ── Replies to replies ── [G1]  G2 ›                children of C1
```

1. **Same ranking as the feed.** Within a row, posts sort by distance tier first, then
   `t_eff` under the reader's preset (§5). Ties use the reader's own edge attributes,
   then ID.
2. **Low-priority posts move out of the way.** The rank-1 post sits in the center
   column, so the vertical scroll path runs through the best post of each generation.
   Lower ranks go to the side, smaller and faded: on the right only on mobile,
   alternating sides on desktop. After 5, a "+N more" chip opens a ranked list, where N
   counts visible posts only.
3. **Swiping a row** re-centers it. The rows below reload as the new center's children,
   and the rows above stay put. A row is ranked as a snapshot, like a feed tier, so it
   never reshuffles under your thumb.
4. **Each post appears once.** Extra visible parents show as chips ("also replies to @b")
   that refocus the view when tapped. If a post would appear a second time, as in a
   diamond, the second spot shows "↑ shown above".
5. **Small section headers** sit on every row: "Earlier", "Replying to", "This post",
   "Replies", "Replies to replies", "3 generations down", and so on. Inside a row, a
   hairline divider marks each change of distance tier.
6. **Loading.** The view opens with the focal post plus 2 rows above and 2 below. More
   rows load near the edges.
   - Each row reads only link IDs from `post_parents`, then filters through the gate.
     Every visible candidate in the row is ranked, and bodies are loaded for the top ~10.
   - Authors deeper than the reader's current search layer come back in the fixed
     latency bucket (§4.1).
7. **Interaction.** On mobile, scroll vertically and swipe each row horizontally, with
   no free pan or zoom. On desktop, zooming out shows a minimap (Phase 1).

#### 10.1.5 Short posts, systematically
Long posts are discouraged with friction only. Nothing ranks them lower, because a
length penalty would hurt dictation users, non-native writers and assistive-tech users,
and it's easy to game.
- **Composer counter.** The counter appears at 280 characters and turns amber at 500,
  the soft limit. There's a **hard cap of 20,000**, for storage and abuse only.
- **Split into a thread.** Above 500 characters, the composer offers to split the post.
  It cuts at paragraph, then sentence, boundaries into a `cont` chain that the author
  previews and adjusts. A split has at most 25 parts, and a thread published in one
  composer action counts as **one write** against the §17 limits.
- **Folding.** Feed cards fold posts longer than 6 lines or 400 characters behind
  "more", and the tree view folds at 10 lines. The fold works by keyboard and screen
  reader.

#### 10.1.6 Markdown & link previews
**Markdown subset.** The supported elements are:
- paragraphs
- `*em*`, `**strong**`, `~~strike~~`, `` `code` ``
- fenced code, collapsed past 10 lines
- blockquotes and lists, each nested up to 2 levels
- `http`/`https` links
- @mentions (§10.6)

What is toned down or left out:
- **Headings render at body size, semibold**, with no extra spacing, so a post that's
  only a heading looks like a normal paragraph.
- **No raw HTML.** It shows as literal text.
- **Images in markdown are never fetched.** `![]()` renders as a plain link; images come
  only through media uploads (§6).
- **Tables** may come in Phase 1.

Rendering rules:
- markdown-it with `html: false` and a node allowlist, emitting React elements. Never
  `innerHTML`.
- A strict CSP and Trusted Types.
- **Every link shows its real host.** Punycode is decoded, and lookalike characters are
  flagged. If the link text looks like a different domain, the true host is shown next
  to it.
- Links carry `rel="noopener noreferrer nofollow ugc"` and send no referrer.

**Link previews.**
- **Built per post, at post time, on the server.** Viewers never contact the target
  site, so there are no viewer IP leaks or tracking pixels.
- **Timing.** The preview appears **only after posting**, after a fixed delay. Each post
  stores its own copy.
- **An internal cache** keyed by canonical URL (TTL ~1 h) avoids refetching viral links.
  Users can't observe it, because of the fixed delay and the per-post copy.
- **Per-host rate limit.** Fetches are rate-limited per target host, so the fetcher
  can't be used to hammer a site.
- **The fetcher is isolated and SSRF-guarded:**
  - http/https on ports 80 and 443 only;
  - private, loopback, link-local, CGNAT and metadata addresses are rejected;
  - the connection is pinned to the checked IP, so DNS rebinding fails;
  - at most 3 redirects, each re-checked;
  - a 3 s and 512 KB cap, HTML only;
  - no cookies.
- **Tracking parameters are stripped** (`utm_*`, `fbclid`, `gclid`, …) before the URL is
  stored or shown.
- **Phase 0 previews are text only:** title, a description of up to 200 characters, and
  the **real host**, which is the only field we trust. From Phase 1, preview images go
  through the media pipeline, including CSAM scanning.
- **Opt-outs.** A post can switch previews off, and wrapping a URL in `<…>` skips it.
  The API never accepts preview data supplied by the client.
- **Reporting.** A "preview misleading" report option handles sites that show the
  fetcher one thing and visitors another.

#### 10.1.7 Feed cards & tier headers
- **Instagram-style cards** in a single column. Each card has:
  - an author row: avatar, handle, inviter label, chain (§4.5) and an ⓘ "why";
  - the context row;
  - the body and the preview;
  - actions: like · reply (opens the composer with this post as parent) · tree.
- **The reply count** counts only visible links.
- **Small sticky tier headers:** "Friends", "Friends of friends", "3 steps away",
  "4 steps away", and so on. Empty tiers get no header.
- **End of a tier.** A divider reads "You're caught up with friends", and the last one
  reads "You're caught up".
- **Headers come from the API** (`tier_start` markers), never from post content, so
  every format and custom layout shows them and nobody can spoof them.
- **DMs** between friends are a later addition.

### 10.2 Edits, deletion & export
- **Edit:** the same post `id` with a new `version` (each version is signed), shown as
  "edited". Parents can't change.
- **Delete:** a signed `Tombstone`, followed by a hard delete of the body and media.
- **Account deletion:** tombstones all posts and revokes all edges. A stub
  `{id, invited_by, invitees}` is kept for **moderators only**. Backups age out (§2.3).
- **Export (Phase 0):** `GET /export` returns three things:
  - `records.jsonl`, the exact signed bytes of every record you're a party to;
  - `keys.json`, the server's public signing keys with validity windows;
  - your own post bodies.

  Edges are mutual, so **two users' exports reproduce the same signed `Friendship`**. A
  successor service, or a fork, could rebuild the consented graph from the users who
  move, without trusting this operator's database. This is the portability federation
  would have given, at almost no cost.

### 10.3 Account recovery
- **Phase 0:** multiple passkeys (encouraged at signup), plus a manual process that needs
  two moderators. Email is optional.
- **Phase 1:** recovery confirmed by **2 friend+ contacts**.
- **Phase 3:** device keys with N-of-M friend+ guardians.
- **Notifications:** friends are told about every recovery or passkey change.

### 10.4 Moderation & reporting
- **Per-user tools:** unfriend, mute, **block** (removes the person from all your paths,
  §4), and **hide-via** (Phase 1; cuts a hop without unfriending).
- **Reports** go to moderators only.
- **After a moderator confirms action**, the author's inviter gets a "blame inbox" entry:
  "a post by your invitee was removed (reason)". The reporter stays anonymous, and the
  content is shown only if the inviter could already see it.
- **The public moderation log** lists action types and counts under pseudonymous IDs. A
  name appears only to viewers who already have that person in their horizon.

### 10.5 Discovery (horizon-safe)
- No global search, no public profiles, and no handle lookup.
- **Ways to connect:**
  1. **In person** (the primary route): scan someone's invite code while signed in
     (§7 "One code, two uses").
  2. **People you may know:** 2nd-degree candidates with ≥2 mutual friends, through
     edges at least 7 days old. Only a count of mutual friends is shown, and anyone can
     opt out of being suggested.
  3. **Requests from wherever you've already seen someone** (a chain, a reply, a
     suggestion), using an opaque per-viewer reference.
  4. **Introductions** by a mutual friend.
- **No oracles.** Every miss returns the same not-found response (§13).
- **Search** covers only posts you can already see.

### 10.6 Notifications & mentions
- **Phase 0:** in-app via SSE. **Phase 1:** Web Push.
- **Events:**
  - friend request
  - request accepted
  - posts referencing yours (batched; §10.1.1)
  - mention
  - moderator-confirmed action on an invitee
  - a friend's passkey or recovery change
- **A declined request looks exactly like a pending one.**
- **Mentions:**
  - `@handle` resolves only if the target is in the **author's** horizon, and is linked
    only for viewers who also have the target in their horizon.
  - Everyone else sees identical plain text, whether or not the account exists.
  - The mentioned person is notified only if they can see the post.

### 10.7 Languages & autotranslation
**User settings:**
- `ui_locale`: the language the app's interface is shown in.
- `languages[]`: **every language the user reads**, which can be more than one.
- `translate_to`: the target language for translations, which must be one of
  `languages`.
- `autotranslate`: off by default, so translation is opt-in.

**Post language.** Every post records `lang` (a BCP-47 code).
- **Detection:** the composer guesses it on-device with a small language-ID model, and
  the author can correct it.
- **Mixed or unknown posts** are stored as `und` and are never auto-translated.
- **Recorded from Phase 0**, so translation can be added later without backfilling.

**Autotranslation (Phase 2+, opt-in).**
- **When it applies:** only when `autotranslate` is on **and** the post's `lang` is
  **not** in the reader's `languages`. Someone who reads Spanish and English never gets
  Spanish or English posts translated.
- **How it's shown:** the translated text carries a mark, "Translated from Spanish ·
  Show original", and the original is always one tap away.
- **What's translated:** posts, link-preview titles and descriptions, and context rows.
  Handles, links, code blocks and mentions are never translated.
- **Per post:** readers can translate any single post on demand, even with autotranslate
  off. Authors can't block translation, because readers could paste the text into a
  translator anyway.
- **When and how it runs:** lazily, when a post is about to be shown. The result is
  cached per `(post_id, version, target_lang)` and dropped when the post is edited or
  deleted.
  - **Translations appear after a fixed delay,** so a cache hit doesn't reveal that
    someone else already read the post in that language (§2.2).
- **No effect on ranking:** translation never changes tier, ranking or engagement.
- **Privacy:** post text is private content. **A self-hosted open translation model is
  preferred.** An outside provider is acceptable only with zero retention and no training
  on the data, and only post text is ever sent: no IDs, authors or graph data (§2.3).

### 10.8 Bootstrapping
The founder plus ~10–20 genesis users are seeded via CLI with `invited_by = genesis`.
They skip probation.

---

## 11. Why not federation (decision)

The README hoped for federation. We're **not federating, for now**. Three hard
requirements (graph privacy, no maximum distance, and exact shortest-distance tiers) are
simple to meet in one service, and costly to meet across servers.

**Simple designs fail.** Passing distances along every cross-server edge, or caching
per-author distances, comes to ~10¹³ entries a day at 1M users and 100 servers.

**The best federated design found** ("ball exchange") works like this:
- Short post headers flood within each connected component, and bodies are fetched on
  demand.
- Each server shares, per user, which active authors lie within k hops. Deeper hops are
  computed only when someone scrolls that far.
- Before a deep post is shown, a signed "trace" walks the hops to verify a fresh shortest
  path.
- The network-wide cost is roughly 10–20 GB/day of headers, ~1–2 GB/day per server, plus
  lazy queries for deep tiers.

**It works, but it costs:**
- **Freshness:** deep tiers are exact only as of a recent snapshot (~1 h). Tiers 1–2
  stay exact, and visibility is checked fresh by trace.
- **Blocks:** they remove someone from your paths only up to 2 hops. Deeper, a blocked
  person can still be an unnamed middle hop.
- **Privacy:** a server with many users learns their distances to every author in their
  component, which is enough to partly reconstruct the graph near them.
- **Bot defense:** a rogue server can mint accounts, fake in-person proofs, and forge
  edges between its bots and its own real users. Containing that needs server
  reputation, witnessed logs, per-server CSAM scanning and a custom protocol (months of
  work).

**What we keep instead:**

| Federation benefit | Single-service substitute |
|---|---|
| No single owner | Co-op or nonprofit governance |
| No lock-in | Open-source code, a published ranking formula, and **portable signed exports** (§10.2) |
| Resilience | Replicas (§12) |
| Protection from the operator | Insider controls and the transparency log (§2.3) |

If federation is revisited, start from "ball exchange". Its single-server behavior
matches §4 exactly, so it could be added without changing what users see. The block and
freshness caveats above are the only differences.

---

## 12. Scaling path

| Users | Shape |
|------:|-------|
| ≤ 5k | Phase 0: one VPS. Full BFS on every feed open |
| ≤ 1M | One large box (~64 GB RAM). The graph is ~600 MB of CSR, the `recent` index ~170 MB. Lazy BFS with epoch caching. Postgres partitions and a read replica. R2 + CDN |
| ≤ 50M | A dedicated graph service: CSR replicated in memory (~30 GB per machine), with BFS sharded by reader. Postgres sharded by author |
| Beyond | Partition the graph by community, with cross-partition BFS. Known territory: Facebook's TAO, LinkedIn's graph services |

**Deep-search budget.** This is the one cost that grows faster than the user count:
- **1M users:** a full search is ~0.3 s of CPU. With 10% of users going deep once a day,
  the load averages well under one core.
- **50M users:** a full search is ~7.5B edge visits, 10–30 s of CPU. The same 10% would
  need hundreds of cores.

So at that scale:
- **Reuse:** keep the one-full-search-per-reader-per-day rule.
- **Truncate:** stop each search once the window's active authors are all labeled.
- **Batch:** run deep searches in a nightly job for users who regularly go deep.
- **Measure** how often tiers 4+ are reached before buying hardware. Most readers never
  get past tier 2.

---

## 13. API, observability, testing, compliance

### Visibility gate (`internal/gate`)
- **Only `gate` imports `store`.** Handlers import `gate`, and a `depguard` rule in CI
  enforces this.
- **`gate` returns view objects** (`PostView`, `PersonView`, `ChainView`) built from
  H(V), so a handler can't even name a raw row.
- **Opaque per-viewer references** are the only way users and posts appear in the API:
  `ref = base64url(AES-SIV(k_ref, viewer_id ‖ kind ‖ target_id))`.
  - They're deterministic, so they cache well.
  - Two viewers get different refs for the same target, so refs can't be correlated.
  - A ref pasted by another viewer fails to decrypt.
- **One failure value.** Every `gate` call returns either a view or `gate.ErrNotFound`.
  One middleware maps that to a single fixed response, and Phase 1 adds timing padding.
  No handler writes its own 403 or 404.

### API & observability
- **API:** an internal JSON REST API (unpublished; §16.1) specified in OpenAPI, with a
  generated TypeScript client for the first-party app, plus SSE.
- **Telemetry:** self-hosted or ID-free (§2.3).
- **Product metrics, aggregates only:**
  - feed depth reached
  - share of impressions by degree
  - invites per day
  - p95 feed latency
  - deep-search rate

### Testing
- **Distance and chains:**
  - The tier always equals a reference BFS distance (with blocks).
  - Chain length equals the tier.
  - A chain never contains two friends who aren't adjacent in it.
- **Visibility:** no post reaches a viewer who has no path, is outside the audience
  limit, or could only be reached through a blocked user.
- **Horizon invariance:** run against `gate`'s exported functions. Changing anything
  outside H(V) must leave V's results identical.
- **Component isolation:** no identifier from another component ever appears in V's
  output, apart from the inviter labels of users V can see (§2.2(4)).
- **Inviter label:** every user V can see carries their inviter label, and the label is
  never an actionable ref. Only "report inviter", routed through the invitee, works.
- **Oracle uniformity:** a nonexistent account, one in another component, and one outside
  V's horizon all produce the same not-found response. Phase 1 adds a latency test
  (Kolmogorov–Smirnov).
- **Ranking:**
  - The ranked feed is a permutation of the unranked one.
  - λ = 0 gives exactly chronological order.
  - Engagement from beyond 2 hops never changes V's order.
- **Synthetic graph:** small-world, power-law graphs of 10k–1M users, for performance and
  bot-isolation simulations.
- **Handshake:** Playwright end-to-end tests with a fake camera stream.
- **Family trees:**
  - Adding, deleting or detaching a child outside H(V) leaves V's tree view
    byte-identical.
  - No placeholder ever appears.
  - Referencing a post you can't see returns the uniform not-found response.
- **Customization and anti-scraping:**
  - Every UI format and every accepted custom layout renders all integrity elements
    (§15.5), and templates that omit them are rejected.
  - Requests without the first-party origin, session and CSRF token get the uniform
    not-found response.
  - From Phase 1, graph-changing writes fail without a first-party passkey assertion.
  - Media URLs fail after expiry or without a signature.
  - Assistive-tech usage profiles never trigger a step-up.

### Compliance
- GDPR: export, deletion and retention (§2.3, §10.2).
- Age gate.
- Terms of service.
- DMCA agent (US).
- CSAM duties (§6).

---

## 14. Social-good stance
- **No ads, and no ranking that optimizes time spent.** Ranking only reorders posts
  within a tier, using published signals from your own network. "Latest" is one tap away.
- **No signals that reward clickbait or outrage**, and no collective down-voting.
- **The feed ends** ("You're caught up"), on purpose, against doomscrolling.
- **Members' content is never AI training data:** it isn't sold, licensed, or used to
  train our own models, and it's actively protected from scrapers (§17.3).
- **Funding:** donations or co-op membership.

---

## 15. Clients & UI formats

### 15.1 One view-model, several layouts
- **The data layer is shared.** The PWA has one data layer: the generated TypeScript
  client produces view-models (`FeedPageVM`, `PostVM`, `TreeVM`).
- **A format is only layout.** It's a set of layout components over those view-models.
  It never fetches its own data, so formats can't differ in what they reveal.
- **Preferences** live server-side in `users.ui_prefs`, so they follow you across devices.
  Each format is a lazily loaded chunk.

| Format | Feed | Family tree |
|---|---|---|
| **Cards (default)** | Clean Instagram-style cards (§10.1.7) | The rows layout (§10.1.4) |
| **Compact** | Dense text rows: author, chain, first lines | An indented outline you can collapse |
| **Explorer** (Phase 1) | Cards with a small tree thumbnail | A pan-and-zoom node canvas, plus an outline mirror for keyboard and screen-reader users |

**Accessibility settings are not a format.** High contrast, text size, reduced motion and
dyslexia-friendly fonts apply to **every** format.

### 15.2 Customization, staged

| Stage | What users control | Safety model |
|---|---|---|
| **A. Themes** (Phase 1) | Design tokens only: color, radius, spacing, density, and fonts from an allowlist | Validated server-side. Integrity elements must keep WCAG AA contrast and a minimum size, or the theme is rejected |
| **B. Layout config** (Phase 2) | A JSON document under a published schema: slot order, density, which optional parts to show, tree depth | Integrity elements are required slots. The schema simply can't express hiding them |
| **C. Custom layouts** (Phase 2+) | Whole feed, card and tree layouts as declarative templates (§16.2) | Rendered by the first-party renderer. Integrity elements are required, and there are no scripts and no network access |

**User-supplied CSS or JS never runs inside the first-party app.** CSS alone could hide
blame information, spoof a chain or a "Verified in person" badge, overlay a fake
passkey prompt, or leak data through selectors and `url()`.

### 15.4 Localization: every user-facing string is a UI string
**Rule: no user-facing text is hard-coded, anywhere.** That covers buttons, tier headers,
"Why am I seeing this?", errors, notifications, Web Push, the composer nudges, and moderation
and blame messages.
- **Message catalogs.** Strings live in catalogs keyed by ID, written in ICU MessageFormat so
  plurals, gender-neutral phrasing and interpolation work in every language. English is the
  only catalog in Phase 0, but nothing bypasses the catalog.
- **The server sends codes and data, not prose.** API responses carry structured data the
  client renders, such as `{tier: 2}`, `{shift_hours: 4, reason: "network_likes"}`,
  `{error: "not_found"}` or `{notification: "post_referenced", count: 5}`. Formats and
  custom layouts (§16.2) all render from the same localized catalogs.
  - The only text the server renders itself is Web Push and other out-of-app messages,
    using the recipient's `ui_locale` and the same catalogs.
  - The uniform not-found response (§13) stays uniform: it's one code, localized on the
    client.
- **Formatting.** Dates, numbers and relative times ("9 h ago") go through `Intl` in the
  user's locale.
- **Layout.** CSS uses logical properties, so right-to-left scripts work without forks.
  Designs leave room for ~40% text expansion, and the sticky tier headers and action rows
  have to survive German and Finnish.
- **Enforced in CI:**
  - A lint rule (e.g. `i18next/no-literal-string`) fails the build on any literal
    user-facing string in the client.
  - A Go check does the same for server-rendered messages.
  - Phase 0 ships a **pseudo-locale** ("[Ŧĥîš îš ŧĥê ƒêêð !!!]"). It's used in tests and
    screenshots to catch hard-coded or truncated text.
- **Not translated:** the handshake jingle. The sound logo is the same everywhere.

### 15.5 Integrity elements
These are visible and legible in every format, theme and layout:
- tier headers
- the chain (§4.5)
- the inviter label (§3)
- "Why am I seeing this?" (§5.3)
- the audience-limit badge
- the "edited" mark
- the report action

---

## 16. Custom UIs without a public API

**There is no public API.** A documented API would mostly serve scrapers. Instead, custom
UIs are built **inside the first-party app**, by people, without code that can send data
anywhere.

### 16.1 The internal API
- **First-party only.** The endpoints exist only for the first-party app. They're
  undocumented, unversioned and change freely. The OpenAPI spec is used internally to
  generate the app's own TypeScript client and isn't published.
- **They're CSRF-protected** (§17.2). There are no bearer tokens, no OAuth, no CORS and no
  webhooks.
- **Responses carry only what the UI displays.** No hidden fields, no bulk endpoints, and
  per-viewer refs (§13).

### 16.2 Custom layouts: the "easy to build" part
This is stage C of §15.2. **Custom layout templates** are a declarative component tree,
stored as JSON with an optional friendly text syntax. They're built from first-party
primitives:
- `Stack`, `Row`, `Grid`, `Card`, `Spacer`, `Divider`
- `Text`, bound to view-model fields such as `post.body` and `author.handle`
- `Media`, `Preview`
- `Chain`, `TierHeader`, `WhyButton`, `InviterLabel`, `Actions`

Bindings, conditions ("show the preview only if…") and per-breakpoint variants are
allowed. Scripts, raw CSS, URLs and anything that loads external resources are not.

- **Integrity elements (§15.5) are required primitives.** The renderer rejects a
  template if any of these is true:
  - it leaves an integrity element out;
  - it places one under a condition;
  - it omits one from any breakpoint variant;
  - it overlaps or covers one;
  - it styles one below the contrast or size floor.

  The integrity elements' reserved styles and icons aren't available to user content, so
  `Text` and static labels can't imitate a chain, a tier header or a "Verified in person"
  badge.
- **Live editor.** An in-app editor shows a live preview against your real feed. Phase 2
  starts with a JSON editor plus the preview; a visual editor comes later.
- **Sharing.** Templates are plain files. Users can export them, import them, and share
  them with friends (for example, attached to a post). Importing shows a preview, and a
  template can't affect anything except your own layout.
- **Localization.** Fixed text in templates uses catalog keys. A user's own static labels
  are treated as user content.
- **Why not user scripts in a sandbox?** Any script that can see the view-model can leak
  it, through navigation, timing or encoded requests. A browser sandbox doesn't reliably
  close all of those channels. Revisit only with a provably network-isolated runtime.

---

## 17. Anti-scraping & one person, one action

### 17.1 Threat model
- **Scraping.** A logged-in account automates its own browser (Playwright, an extension)
  to harvest its horizon. Or many cooperating accounts pool their horizons to rebuild
  part of the graph.
- **Automated posting.** Bots, or LLM agents driving a real account.

What can't be prevented: a person who patiently browses at human pace can record what
they see. That's their own horizon, which they're entitled to. The defenses below make
**bulk** and **aggregated** scraping slow, detectable and attributable. Pooling horizons
needs many real, in-person-invited accounts, each with an inviter on record.

### 17.2 Defenses
1. **CSRF protection, not an anti-scraping measure.** Requests need a `SameSite=Strict`
   HttpOnly session cookie and a CSRF token, and there's no CORS. A scraper outside the
   browser can replay all of these, so the real anti-scraping defenses are the ones
   below.
2. **Device-bound sessions, where supported (Phase 1).** Logging in requires a passkey.
   - **On Chromium with a TPM**, the session is bound to a device key through Device
     Bound Session Credentials, so a copied cookie is useless on another machine.
   - **Elsewhere**, sessions are short and refresh only after re-verification.
3. **Read budgets per account:**
   - feed pages
   - tree views
   - a lower budget for graph-revealing reads (people, chain expansion, suggestions)
   - full-resolution media (§17.3)

   They're set at roughly what a human uses in a day. Every request is charged the same,
   whatever the outcome (found, not found, forbidden), and hitting a limit returns a
   uniform 429, so limits reveal nothing about targets.
4. **Automation signals.** None of these blocks anyone by itself.
   - **Read patterns:**
     - sustained page rate with no idle periods, or round-the-clock activity
     - opening every node of a tree breadth-first
     - expanding chains or people at machine speed
     - reads with no matching seen events
   - **Browser signals:**
     - `navigator.webdriver`
     - signs of automation through the DevTools protocol
     - no input events behind sustained activity
   - **Cross-account correlation:** read patterns that line up across accounts, or
     shared IP ranges, ASNs or device fingerprints.
5. **Passkey step-up.** A session that trips the signals has to re-verify with a passkey,
   which requires a human tap. Repeated step-ups, or clusters across accounts, go to
   moderators (§9). The invite tree makes clusters attributable.
6. **Accessibility first.** Screen readers, keyboard-only use and assistive tech must
   never trip the signals. Thresholds are tuned against them, and signals only ever
   trigger a step-up, never a ban.
7. **No third-party scripts on app pages.** CDN features that inject scripts are disabled
   on app paths: JavaScript bot challenges, Zaraz, Rocket Loader and analytics beacons.
   Edge rate limiting and WAF rules don't need them.

### 17.3 Scraping for AI training
The goal is that members' posts and media **never become training data**, unlike
platforms whose content is public or whose operators license it.

1. **Nothing is public.** No public pages, profiles, posts, media or handle lookups
   (§9, §10.5). Ordinary crawlers get a landing page and nothing else. That's the main
   defense, and the reason not to have a public API.
2. **Media URLs don't leak usefully.**
   - **Signed per viewer** after the gate checks access, with **~1 h expiry**. The client
     re-requests a URL when it gets a 403, so long-open pages keep working.
   - **Random keys**, so URLs can't be enumerated.
   - **Edge validation.** A Worker or a WAF rule at the CDN edge checks the HMAC, and the
     **cache key excludes the signature**, so caching still works. A URL pasted into a
     public forum stops working within the hour.
   - Signatures are the control. Referer and Origin rules aren't used, because the app
     sends no referrer and image requests carry no `Origin`.
3. **Members who scrape** hit §17.2: read budgets, automation signals and step-up.
   - **Media budget.** Full-resolution media has its own, tighter budget. The feed loads
     display-size images, and the original only on tap.
   - **Attribution.** Bulk downloading is attributable to an account and its inviter.
4. **Forensic watermarking** (Phase 2+, opt-in per author, for images and video).
   - **How it works:** A/B variant watermarking. Each image tile or HLS segment is
     pre-rendered in two invisibly different variants, and each viewer gets a unique
     sequence of variants. That stays cacheable, and a leaked copy can be traced back
     to the account that fetched it.
   - **Coverage:** it applies to display sizes **and** the full original.
5. **Optional protective cloaking (Phase 2+).** Artists can opt in to Glaze-style
   adversarial perturbation on upload. It's an arms race, not a guarantee, so it's
   offered rather than promised.
6. **The legal and policy backstop:**
   - **TDM opt-out.** Every response carries a W3C TDMRep `tdm-reservation: 1` header,
     the EU DSM Directive Art. 4 opt-out, and `tdm-policy` points at the terms.
   - **`robots.txt`** disallows everything, naming AI crawlers explicitly.
   - **Best effort only:** `X-Robots-Tag: noai, noimageai` and an `ai.txt` file. They're
     non-standard but cheap.
   - **The terms of service** forbid scraping and AI training.
   - **We never license or sell member content for AI training**, and we don't train
     models on it ourselves (§14).
   - **Outside AI services** are used only under zero-retention, no-training terms
     (§10.7).
7. **No cost amplification.**
   - **Hosting:** fixed-price servers behind a CDN, not per-request serverless billing.
   - **Unauthenticated traffic** is served from cache or a static page, and logged-out
     scraping has nothing to hit.
   - **Rate limits** at the CDN edge stop traffic before it reaches the app.

### 17.4 One person, one action
**Definition.** Every write is a **deliberate act by the account's own human, at human
pace**. No auto-likes, auto-replies, auto-accepting, or scheduled or generated posts sent
without a per-post human action.

- **Human-pace write limits per account.** Starting points:
  - 30 posts a day and 10 an hour (a thread published in one action counts as one)
  - 100 likes an hour
  - a minimum spacing between writes
  - the reference caps from §10.1.1
- **Graph and account changes** (creating or revoking edges, blocks, passkey changes,
  recovery, deletion) need a fresh passkey assertion from Phase 1. Its
  `clientDataJSON.origin` must exactly match the first-party origin, and no third-party
  code is ever served under that domain.
- **Invites, handshakes and export** happen only in the first-party app's dedicated
  screens.
- **The §9 behavioral signals** apply: cadence, cross-account similarity, and invite-tree
  clustering, plus step-up as in §17.2.

---

## 18. Build order

| Phase | Scope | Infra |
|------:|-------|-------|
| 0 | **Accounts and invites:** text only; passkey login; genesis users seeded by CLI; in-person invites (rotating QR + session-bound token); token-bucket invite budget and Phase 0 probation.<br>**Graph and feed:** bidirectional friendships and block; shortest-distance tiers with unlimited reach (full BFS per feed open); shortest-path chains collapsed per §4.5; optional audience limit; ranking subset (§5.5).<br>**Posts:** Cards and Compact formats with tier headers; `parents[]` references and `cont` threads; tree view (mobile rows); Markdown subset; text-only link previews; composer counter and split-into-thread.<br>**Safety and plumbing:** `gate` with opaque refs and uniform not-found; in-app SSE notifications; delete post or account; export; manual recovery; one server signing key; internal CSRF-protected API, read budgets, `robots.txt`/TDM opt-out headers.<br>**Localization groundwork:** every user-facing string in catalogs (English + pseudo-locale), lint in CI, API returns codes and data, `posts.lang` recorded, `languages[]` in settings | Go + Postgres + SPA, one VPS |
| 1 | Per-user keys and passkey-co-signed edges; two-layer handshake sound (jingle + ultrasonic), handshake score, and jingle sound design; friend+ and friend+ recovery; hide-via; images (scanning, signed URLs); Web Push; behavioral signals and moderation queue; saves, grandchild upward credit and the Top preset in ranking; Explorer format and themes; device-bound sessions, automation signals and passkey step-up; tight signed media URLs; preview images; desktop minimap; invite diversity decay; timing padding; lazy BFS and epoch caching once measured | + R2, + Valkey if measured |
| 2 | Additional UI languages; opt-in autotranslation (§10.7); layout config (§15.2 B) and custom layout templates (§16.2); opt-in forensic watermarking and cloaking (§17.3); trust scores (collapse, engagement weight); transparency log; author baseline in ranking; video (single 720p HLS) | + media workers |
| 3 | Native app (UWB proximity, device keys, verifiable client); device-key guardians; graph service split-out if load demands it | as needed |

**Deliberately deferred out of Phase 0** (none of these change the core experiment):
- Lazy BFS, session caching, and in-place CSR updates. At ≤5k users, rebuild the
  adjacency lists on each edge change.
- `seen` partitioning. Use a nightly delete instead.
- Per-user keys.
- Invite diversity decay.
- Hide-via.
- Timing padding.
- The Top preset.

### Phase 0 definition of done
- [ ] Genesis users are seeded by CLI. Everyone else joins only via an in-person
      rotating-QR token.
- [ ] Invite budget and probation are enforced server-side.
- [ ] Friendships need both sides to accept. Unfriend and block take effect on the next
      load.
- [ ] Feed: unseen tier 1 → 2 → 3 → … at **exact shortest distance**, with no maximum,
      then "caught up".
- [ ] Every feed item shows a shortest-path chain, collapsed per §4.5.
- [ ] "Latest" is exactly chronological. "Balanced" follows §5.5.
- [ ] Horizon tests pass on `gate`: a user in a separate component gets responses
      identical to a world where the other component doesn't exist.
- [ ] A seeded bot with zero real edges never appears in any feed.
- [ ] On a synthetic 10k-user graph, tiers 3–6 match a reference BFS, the §4.2 planner
      picks the cheaper scan, and p95 feed open is under 50 ms.
- [ ] Export produces records that verify against the published keys.
- [ ] Posts can reference up to 3 parents. The tree view ranks each row like the feed, and
      the tree noninterference tests pass (§13).
- [ ] Markdown headings render at body size, and link previews are built server-side,
      per post.
- [ ] The whole app renders in the pseudo-locale with no untranslated or truncated text,
      and the string lint passes.
- [ ] 20 real people use it for 2 weeks. Measure invites per day, feed depth, the mix of
      posts by degree, and preset switching.

---

## 19. Open questions (with recommended defaults)

| Question | Recommended default |
|----------|---------------------|
| ~~Should authors be able to limit their own audience?~~ | **Resolved: yes**, because the README asks for it. It's a per-post opt-in (1st, 2nd, 3rd, or everyone connected), defaulting to everyone connected. The system itself imposes no maximum |
| Strict "unseen first", or reserve some slots for farther tiers? | Keep strict tiers, as the README says, and measure. **Needs Corbin's call** before any change |
| Is "aggregate like counts from within 2 hops" (§2.2(6)) an acceptable disclosure? | Yes. Counts only, never names beyond friends. The alternative is no counts at all |
| Default ranking preset? | Balanced, with Latest one tap away |
| Friend cap? | 1,000 |
| What does friend+ unlock? | A friend+-only audience option; it's preferred in chains and tie-breaks, counts toward leaving probation, and acts as recovery guardian |
| What happens to the inviter of a confirmed bot? | Graduated penalties, direct inviter only, moderator-confirmed (§8) |
| Feed window? | 7 days for the feed. Older posts stay reachable on profiles and threads, subject to visibility |
| ~~Update the README?~~ | **Done.** Requirements clarified and extended; federation dropped (§11) |
| ~~Public API?~~ | **Decided: no.** Custom UIs are built in-app with declarative layouts (§16). Scraping, including for AI training, is fought in §17 |
| ~~Filter replies out of the feed?~~ | **Decided: no.** Replies are ranked with a reply penalty, and posts get capped upward credit from well-liked replies (§5.1) |
| Can a parent's author detach children? | Yes (§10.1.1) |
| Scheduled posts? | No. They conflict with one person, one action |
| Translation engine? | A self-hosted open model. An outside provider only with zero retention and no training on the data (§10.7) |
| Which UI languages come after English? | Decide from where the first users are |
| Hard post-length cap? | 20,000 characters, for cost only. The soft limit of 500 does the discouraging |

---

## 20. Changes

### v3.2 → v4
- **Posts instead of comments.**
  - Any post can reference up to 3 parents (`ref`), plus `cont` continuations for
    threads. Together they form a DAG, the post's "family tree" (§10.1).
  - Anti-pile-on controls: a per-post reference policy (including **slippery**: nobody
    can reference it), detach, shadow links for blocked pairs, and reference caps.
- **Instagram-style feed cards** with small tier headers. The family-tree view ranks each
  row exactly like the feed, and lower-priority posts move to the side.
- **Replies are ranked, not filtered.** They carry a reply penalty. A post gains
  PageRank-like **upward credit** from how much your network liked its replies (and
  their replies), capped at its own preference. Both terms disappear under Latest
  (§5.1).
- **Short posts are encouraged by friction only:** a counter, a 500-character soft
  limit, split-into-thread, and folding. There's no ranking penalty.
- **Markdown subset:** headings render at body size.
- **Link previews** are built server-side after posting and SSRF-guarded.
- **UI formats:** Cards (default), Compact, and Explorer.
- **Customization**, staged: themes, then layout config, then **custom layout templates**
  built in-app (§16). Integrity elements can never be hidden.
- **No public API.** The internal API is CSRF-protected and unpublished.
- **Anti-scraping (§17):**
  - device-bound sessions
  - per-account read budgets with uniform charging
  - automation signals with passkey step-up
- **AI-training protection (§17.3):** nothing is public; short-lived signed media URLs;
  opt-in forensic watermarking and cloaking; TDM opt-out headers; and a commitment never
  to license or train on members' content.
- **One person, one action:** human-pace write limits, plus a passkey for graph changes.
- **Localization (§15.4):**
  - every user-facing string comes from ICU catalogs;
  - the server returns codes and data rather than prose;
  - CI runs a lint check and a pseudo-locale;
  - right-to-left scripts are supported through logical CSS.
- **Languages (§10.7):**
  - each post records its language;
  - users list all the languages they read;
  - opt-in autotranslation (Phase 2+) applies only to posts outside those languages.

### v3.1 → v3.2
- **"Invited by" is always visible** to anyone who can see a user, even when the inviter
  is outside the viewer's horizon. It's a display-only tag, and the only action is
  "report inviter" to moderators.
- **An invite also creates the friendship** (friend+ from Phase 1). Unfriending removes
  the friendship, never the invite record.
- **The handshake sound has two layers:** a near-ultrasonic layer and an optional
  audible jingle. The jingle is a branded motif followed by a tail unique to each
  handshake. Both feed a handshake score: Verified (friend+), Accepted or Retry.
  There is an accessibility fallback (§7).
- **The invite screen** shows saved-up invites, or a countdown to the next one (§8).
- **Author audience limits are kept**, because the README asks for them.

### v3 → v3.1 (final review round)
- **Chain choice and tie-breaks** now use only the reader's own edge attributes, not
  trust. Trust is computed per connected component.
- **Like counts and ranking** are limited to engagers within 2 hops. That disclosure is
  added to the horizon rule as §2.2(6).
- **Several leaks closed:**
  - mentions are resolved within the author's horizon only;
  - replies on audience-limited posts are shown only from the viewer's friends;
  - quarantine marks and blame notifications appear only after moderator confirmation;
  - deep tiers are released at a fixed latency.
- **Caching:** a global removal epoch invalidates cached searches. Memory is sparse up to
  layer 3, and there's an explicit deep-search budget (§12).
- **One candidate algorithm for all tiers** (author scan or time scan, whichever is
  cheaper). Whole tiers are ranked, with no early stop.
- **The `gate` module and opaque per-viewer refs** make endpoints horizon-safe by
  construction.
- **Phase 0 trimmed**, with export and manual recovery added.
- **§11 corrected:** federation is feasible ("ball exchange"), but with stated
  compromises. It's not impossible.
- **A note on why not a graph database.**

### v2 → v3

| v2 | v3 | Why |
|----|----|-----|
| Federation with edge replication | A single service (§11) | Graph privacy, unlimited reach and exact distances are simple in one service and costly across servers |
| Graph visible to operators and through side channels | The graph-horizon rule (§2.2), with noninterference tests | The graph must be visible only through connections |
| Public handle resolution; requests by handle | No public lookup; requests only from where you've seen someone; uniform not-found | Both were oracles |
| `max_distance` (default 3 proposed); a "far" bucket | **No system maximum**, exact tiers at every depth, and an optional author limit | Your requirement |
| Reader-side bitmaps, up to 4 hops | In-memory graph and per-reader BFS | Exact at any depth |
| "Newest first" within a degree | Bounded time-shift ranking from your own network | Your request, hardened against collusion, brigading and outrage |
| Federation-only machinery | Removed; insider controls and a transparency log instead | No longer needed |

### v1 → v2 (summary)
- **Feed delivery:** push to 1st degree only, instead of 2nd. Pushing to 2nd degree
  would have meant ~2×10¹⁰ writes a day.
- **Distances and checks:** exact distances, with fail-closed audience checks.
- **Trust model:** made explicit, with passkey co-signing.
- **Handshake:** now a working specification.
- **Invites:** probation and a token bucket. Invite trees grow exponentially.
- **CSAM:** the scanning section was corrected.
- **Phase 0:** trimmed.
- **New sections:** replies, deletion, recovery, moderation, discovery, notifications,
  genesis, testing and compliance.
