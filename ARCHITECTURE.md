# Architecture Sketch (draft v3.2)

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
- **Phase 0 is tiny:** one Go binary, Postgres, and a static web app on one cheap VPS.

> Changes from v1, v2 and v3 are summarized in §17.

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
  need (§15).

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
7. On a post with an audience limit, the replies V sees come only from V's own friends
   (§10.1).

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
Post        { id, author, body, media: [...], audience?, reply_to?, root_id?, supersedes?, created_at, sig }
Tombstone   { target, by, at, sig }
Block       { blocker, blocked, at }        -- never shown to the blocked person
Report      { reporter, target, reason, at }
```

**Invites and "invited by":**
- **An invite creates two records:** the permanent `Invite` and a `Friendship`. From
  Phase 1 that friendship starts as friend+, since an in-person handshake is exactly
  what friend+ means.
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
      name_in_chains bool, suggestible bool, named_in_likes bool)
edges(user_id bigint, friend_id bigint, level smallint, since timestamptz,
      PRIMARY KEY (user_id, friend_id))                              -- both directions
edge_events(id uuid, a bigint, b bigint, kind text, at timestamptz, record_hash bytea)
invites(id uuid, inviter_id bigint, invitee_id bigint UNIQUE, created_at timestamptz, record_hash bytea)
posts(id uuid, author_id bigint, body text, audience smallint /* NULL = everyone connected */,
      reply_to uuid, root_id uuid, created_at timestamptz, deleted_at timestamptz)
  -- INDEX (author_id, created_at DESC), INDEX (root_id)
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
t_eff = created_at + clamp( λ · τ · ln( (1 + n) / (1 + b) ), −S, +S )

n = network preference for this post, as seen from the reader (§5.2)
b = the same author's median n over their previous 10 posts, excluding this one,
    from the same reader; b = 0 if the author has fewer than 3 previous posts
τ = 24 h        S = λ · τ · ln 4   (the most any post can move)
```

| Preset | λ | Max shift | Meaning |
|---|---|---|---|
| Latest | 0 | 0 | Pure chronological order |
| **Balanced (default)** | 0.5 | ±17 h | Recency first, nudged by your network |
| Top (Phase 1) | 1 | ±33 h | Preference matters more |

**Balanced in plain language:** a post moves up about **8 hours each time the number of
people in your network who liked it doubles**, up to a maximum of 17 hours. Latest moves
nothing.

- **Ties** go to friend+ first-hop, then ID. Trust plays no part, because it's global data.
- **Cold start:** a post younger than 2 h never gets a negative shift.
- **Author cap:** at most 2 posts from the same author in any 10 consecutive slots.
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
| Reply | +2 | Once per user. **Controversy damping:** a post's total reply credit is at most 2·(likes + saves) + 2 (Phase 1) |

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

### 5.4 Cost
- About 10⁷ engagement rows a day at 1M users.
- Scoring a tier costs one distance lookup per engager, plus a roaring bitmap
  intersection for hot posts.
- Impressions aren't counted for ranking.

### 5.5 Phase 0 subset
- `n` is the number of distinct people within 2 hops who liked the post, weighted 1 for
  friends and 0.5 for 2nd degree.
- A reply counts only if its author also liked the post, so outrage threads gain nothing.
- `b = 0`.
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

### 10.1 Replies & threads
- A reply is a `Post` with `reply_to` and `root_id`, placed at **its own author's**
  distance from you.
- You see a reply iff you can see the root **and** you're within the reply author's
  audience limit, measured from that author. (Seeing the root already means you're
  within the root's limit.)
- **Audience-limited roots:** on a post with an audience limit, you see only replies from
  your own friends. Otherwise, a reply would reveal that the replier and the root author
  are friends.
- **Hidden replies are simply absent.** No counts.
- **Phase 0:** flat, chronological threads.
- **Later:** nesting, and DMs between 1st-degree friends.

### 10.2 Edits, deletion & export
- **Edit:** a new `Post` with `supersedes`, shown as "edited".
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
  1. **In person** (the primary route).
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
  - reply
  - mention
  - moderator-confirmed action on an invitee
  - a friend's passkey or recovery change
- **A declined request looks exactly like a pending one.**
- **Mentions:**
  - `@handle` resolves only if the target is in the **author's** horizon, and is linked
    only for viewers who also have the target in their horizon.
  - Everyone else sees identical plain text, whether or not the account exists.
  - The mentioned person is notified only if they can see the post.

### 10.7 Bootstrapping
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
- **API:** JSON REST specified in OpenAPI, with a generated TypeScript client, plus SSE.
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
- **Funding:** donations or co-op membership.

---

## 15. Build order

| Phase | Scope | Infra |
|------:|-------|-------|
| 0 | **Accounts and invites:** text only; passkey login; genesis users seeded by CLI; in-person invites (rotating QR + session-bound token); token-bucket invite budget and Phase 0 probation.<br>**Graph and feed:** bidirectional friendships and block; shortest-distance tiers with unlimited reach (full BFS per feed open); shortest-path chains collapsed per §4.5; optional audience limit; ranking subset (§5.5); flat replies.<br>**Safety and plumbing:** `gate` with opaque refs and uniform not-found; in-app SSE notifications; delete post or account; export; manual recovery; one server signing key | Go + Postgres + SPA, one VPS |
| 1 | Per-user keys and passkey-co-signed edges; two-layer handshake sound (jingle + ultrasonic), handshake score, and jingle sound design; friend+ and friend+ recovery; hide-via; images (scanning, signed URLs); Web Push; behavioral signals and moderation queue; saves, replies and the Top preset in ranking; invite diversity decay; timing padding; lazy BFS and epoch caching once measured | + R2, + Valkey if measured |
| 2 | Trust scores (collapse, engagement weight); transparency log; author baseline in ranking; video (single 720p HLS) | + media workers |
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
- [ ] 20 real people use it for 2 weeks. Measure invites per day, feed depth, the mix of
      posts by degree, and preset switching.

---

## 16. Open questions (with recommended defaults)

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

---

## 17. Changes

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
