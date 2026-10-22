# purple-chicken-cult
## Corbin's plan:
### Core Requirements:
A bot-free social media site that works for social good.
- Must be bot-free
- Invite-only
- Strict invite rate-limiting: 1 invite per 24 hours (up to 3 can be saved up), and no invites during a new account's probation period
  - If you're out of invites, you see a countdown to when you can next invite someone
- Invite blame
  - The person you were invited by is clearly shown on your profile so if someone invites a bunch of bots other people can go complain about them
    - This stays visible permanently, even after you remove them, but it's display-only: no further interaction with the inviter is possible through it (complaints go to moderators)
  - Invites must happen in real life: a dynamic code you scan in person, confirmed by a short handshake sound (an optional, recognizable audible jingle plus near-ultrasonic audio)
  - An invite also connects inviter and invitee: as friends when the handshake verified you're together, otherwise as acquaintances
  - The same in-person code works for people who already have an account: scanning it while signed in makes you friends, without spending an invite
- Network graph based connections
  - You see content in the order of your connection to people - that is, if a 1st degree connection posts something, it will always come before a 2nd degree or 3rd degree connection (among posts you haven't seen yet). Additionally, you never see content from someone you have no connection to. This means that if a bot ends up on the platform, as long as all the real people remove it (or the people who invited it) it will become isolated and never have any content reach a real human.
  - A post's connection level is always your *shortest* path to its author (never A→B→C→D when A and D are connected), and that shortest chain is shown with the post
  - There is no system-wide maximum distance: a post can reach anyone connected to its author
  - Within a connection level, posts are ranked by a mix of recency and the preferences of people in your own network (you can switch to pure chronological)
  - All connections/acquaintance requests must be bi-directional. That is, for an algorithmic link to be made, both sides of a request must OK the connection.
  - Allow people to limit posts by connection level: authors may optionally select the maximum distance (1st, 2nd, 3rd degree, or everyone connected) at which their post may be viewed
  - Two levels of connection: an **acquaintance** is anyone you're connected to (a request both sides accepted), and a **friend** is an acquaintance you've verified in real life (the same in-person handshake as invites)
- The connection graph is private: it's only visible through your own connections, and if you're in a disconnected part of the network you can't see anything about the rest
- ~~Ideally distributed/federated~~ Decided against federation for now: it conflicts with graph privacy, unlimited reach, and exact shortest distances (see ARCHITECTURE.md §11). Exports of your own signed records keep the door open.
- Content-wise, something between Reddit, Instagram, and Twitter
  - Instagram-style feed, with small section headers at each connection level
  - Clicking a post opens it as part of a network of posts, like a family tree. Connected posts are prioritized with the same ranking system as the feed, and lower-priority posts are pushed off to the sides
  - No comments: every response is a post, which can reference one or more parent posts. Threads are posts continuing your own post
  - Posts can be made "slippery" (nobody else can reply to them), and authors can detach replies from their posts
  - Replies show up in the feed like any other post but rank a bit lower. A post gets a boost when people in your network like its replies (capped, so posts nobody liked don't rise because of replies dunking on them)
  - Within a connection level, ranking only uses the preferences of people in your own network, never view time, and can be switched to pure chronological
  - Mostly short posts: long posts aren't blocked, but they're systematically discouraged (soft limit, "split into a thread", folding)
  - No titles outside the post
  - Markdown and link previews are allowed, with headers toned down so header text doesn't become the only thing people send
  - Start out with just text as a PoC and see where it goes from there
- No groups or communities: the connection graph acts as a natural proxy for communities
- Several UI formats to pick from, with a sensible, clean default. Advanced users can build their own layouts inside the app
- No public API: it would mostly make scraping easy. Fight bot scraping (and scraping for AI training) as hard as possible
  - Nothing is public, and members' content is never sold, licensed, or used for AI training
- One person, one action: every post, like and connection is a deliberate act by the account's own human
- Multilingual: every user-facing string is a UI string so the app can be translated
  - Later: opt-in autotranslation of posts into the user's primary language. Users can list several languages they read, and posts in any of those are never translated

### Technical Requirements:
- Must be able to scale fast in a theoretical best-case
- Must be user-friendly (ie have a modern-feeling web interface)
- Must be economical

See [ARCHITECTURE.md](ARCHITECTURE.md) for the design.
