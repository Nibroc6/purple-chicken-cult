# purple-chicken-cult
## Corbin's plan:
### Core Requirements:
A bot-free social media site that works for social good. 
- Must be bot-free
- Invite-only?
- Strict invite rate-limiting (maybe 1 invite/24 hours?)
- Invite blame
  - The person you were invited by is clearly shown on your profile so if someone invites a bunch of bots other people can go complain about them
  - Perhaps force invites to happen in real life? Say, some sort of dynamic code that you have to scan to get in.
- Network graph based connections
  - You see content in the order of your connection to people - that is, if a 1st degree connection posts something, it will always come before a 2nd degree or 3rd degree connection. Additionally, you never see content from someone you have no connection to. This means that if a bot ends up on the platform, as long as all the real people unfriend it (or the people who invited it) it will become isolated and never have any content reach a real human.
  - All connections/friend requests must be bi-directional. That is, for an algorithmic link to be made, both sides of a request must OK the connection.
  - Allow people to limit posts by connection level? Basically allow people to select the maximum distance (aka 1st degree connection, 2nd degree connection, etc) at which their post may be viewed.
  - Do we extend the real-life requirement to invites too? Maybe a "friendship+" option that requires real-life verification?
- Ideally distributed/federated, but that can come later for now (just make sure to plan for it in the architecture)
- Content-wise, something between Reddit, Instagram, and Twitter (maybe a twitter-style feed, but make threads based on posts really easy?)
  - Or maybe start out with just text (though don't limit size too strictly) as a PoC and see where it goes from there?

### Technical Requirements:
- Must be able to scale fast in a theoretical best-case
- Must be user-friendly (ie have a modern-feeling web interface)
- Must be economical
