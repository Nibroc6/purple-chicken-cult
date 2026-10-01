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
    - This stays visible permanently, even after an unfriend, but it's display-only: no further interaction with the inviter is possible through it (complaints go to moderators)
  - Invites must happen in real life: a dynamic code you scan in person, confirmed by a short handshake sound (an optional, recognizable audible jingle plus near-ultrasonic audio)
  - An invite also creates a friendship between inviter and invitee
- Network graph based connections
  - You see content in the order of your connection to people - that is, if a 1st degree connection posts something, it will always come before a 2nd degree or 3rd degree connection (among posts you haven't seen yet). Additionally, you never see content from someone you have no connection to. This means that if a bot ends up on the platform, as long as all the real people unfriend it (or the people who invited it) it will become isolated and never have any content reach a real human.
  - A post's connection level is always your *shortest* path to its author (never A→B→C→D when A and D are connected), and that shortest chain is shown with the post
  - There is no system-wide maximum distance: a post can reach anyone connected to its author
  - Within a connection level, posts are ranked by a mix of recency and the preferences of people in your own network (you can switch to pure chronological)
  - All connections/friend requests must be bi-directional. That is, for an algorithmic link to be made, both sides of a request must OK the connection.
  - Allow people to limit posts by connection level: authors may optionally select the maximum distance (1st, 2nd, 3rd degree, or everyone connected) at which their post may be viewed
  - A "friendship+" level requires real-life verification (the same in-person handshake as invites)
- The connection graph is private: it's only visible through your own connections, and if you're in a disconnected part of the network you can't see anything about the rest
- ~~Ideally distributed/federated~~ Decided against federation for now: it conflicts with graph privacy, unlimited reach, and exact shortest distances (see ARCHITECTURE.md §11). Exports of your own signed records keep the door open.
- Content-wise, something between Reddit, Instagram, and Twitter (maybe a twitter-style feed, but make threads based on posts really easy?)
  - Start out with just text (though don't limit size too strictly) as a PoC and see where it goes from there

### Technical Requirements:
- Must be able to scale fast in a theoretical best-case
- Must be user-friendly (ie have a modern-feeling web interface)
- Must be economical

See [ARCHITECTURE.md](ARCHITECTURE.md) for the design.
