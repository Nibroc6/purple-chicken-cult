# Egress: link-preview and preview-image fetching

Owner: P0-W09 → P1-W11. Applies to every environment that fetches link previews.

## What fetches what

Only the server fetches previews (ARCHITECTURE §10.1.6). Viewers never contact a
linked site. The fetcher is `internal/platform/safefetch`, called by the `preview.fetch`
job (`internal/gate/previews`) once per post version, for at most 3 links. Each fetch:

- uses `http`/`https` on ports 80 and 443 only, and refuses URLs with userinfo;
- ignores `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` (no proxy is ever used), keeps no
  cookies and sends `User-Agent: pcc-preview/1`;
- resolves the host, refuses the whole host if any answer is in the deny list below,
  connects to the checked IP only, and checks the socket address again just before the
  connection is made (so DNS rebinding cannot reach an internal address);
- refuses numeric host spellings other than dotted quads (`2130706433`, `0x7f.1`,
  `0177.0.0.1`, `127.1`), single-label names, `localhost`, `*.local`, `*.internal`,
  `*.localhost`, `*.lan`, `*.home.arpa` and the cloud metadata names;
- follows at most 3 redirects and checks each one from scratch;
- stops after 3 s in total, reads at most 16 KiB of headers, keeps at most 512 KiB of
  the body after gzip decoding, and accepts `text/html` only.

From Phase 1 (P1-W11) the same job also fetches the page's `og:image` (or `twitter:image`),
resolved against the page's final URL and stripped of tracking parameters, through
`safefetch.GetImage`: every rule above applies, again from scratch (the image host may differ
from the page's), except that it accepts only `image/png`, `image/jpeg`, `image/gif`,
`image/webp` and `image/avif` (never SVG) and refuses any image larger than 5 MiB after gzip
decoding instead of truncating it. The image counts against its own host's limiter budget.
It is fetched only while media is enabled (`MediaEnabled`, plan D20), once per post, and goes
straight into `Media.Ingest`, which runs the same CSAM scan as an upload: a match is preserved
and queued for moderators (`deploy/runbooks/csam.md`) and the preview shows no image. Viewers
only ever load the stored copy through signed media URLs.

The deny list: `0/8`, `10/8`, `100.64/10`, `127/8`, `169.254/16`, `172.16/12`,
`192.0.0/24`, `192.0.2/24`, `192.88.99/24`, `192.168/16`, `198.18/15`, `198.51.100/24`,
`203.0.113/24`, `224/4`, `240/4`, `255.255.255.255`, `::`, `::1`, `::/96`,
`::ffff:0:0/96`, `64:ff9b::/96`, `64:ff9b:1::/48`, `100::/64`, `2001::/32`,
`2001:db8::/32`, `2002::/16`, `fc00::/7`, `fe80::/10`, `fec0::/10`, `ff00::/8`.

Fetches are also limited per registrable domain (1 request per 2 s, burst 3), and the
results are cached for 1 hour in `link_fetch_cache`. Preview rows become visible only at a
fixed delay bucket (`PCC_PREVIEW_DELAY`, default 30 s), so fetch timing is never visible to
users.

## Defense in depth: firewall the service user

The application checks are the primary control. Add an egress firewall anyway, so a bug in
the fetcher still cannot reach internal services. Run `pcc` as its own system user (for
example `pcc`), then allow that user outbound TCP 80/443 to public addresses only, plus
whatever the deployment needs (Postgres, DNS, SMTP or the object store in Phase 1).

nftables example (adjust the Postgres address and the uid):

```nft
table inet pcc_egress {
  set private4 {
    type ipv4_addr; flags interval
    elements = { 0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16,
                 172.16.0.0/12, 192.0.0.0/24, 192.168.0.0/16, 198.18.0.0/15, 224.0.0.0/4,
                 240.0.0.0/4 }
  }
  set private6 {
    type ipv6_addr; flags interval
    elements = { ::/127, ::ffff:0:0/96, 64:ff9b::/96, 2001::/32, 2002::/16, fc00::/7,
                 fe80::/10, fec0::/10, ff00::/8 }
  }
  chain output {
    type filter hook output priority 0; policy accept;
    meta skuid != "pcc" accept
    oif "lo" tcp dport 5432 accept                 # local Postgres, if any
    udp dport 53 accept                            # the resolver
    tcp dport 53 accept
    ip daddr @private4 drop
    ip6 daddr @private6 drop
    tcp dport { 80, 443 } accept
    drop
  }
}
```

If Postgres or the resolver lives on a private address, add an `accept` for exactly that
address and port **above** the `@private4` drop.

Check it as the service user:

```sh
sudo -u pcc curl -sS -m 3 http://169.254.169.254/ ; echo "exit $?"   # must fail
sudo -u pcc curl -sS -m 3 -o /dev/null https://example.com/ ; echo "exit $?"  # must succeed
```

## Test environments

Under `PCC_ENV=test` with `PCC_TEST_HOOKS=1` (end-to-end runs only; `config.Validate` refuses
test hooks in any other environment), the fetcher may also reach `127.0.0.1` and `::1` on any
port, so the Playwright specs can serve pages locally. Every other rule still applies. Never
set `PCC_TEST_HOOKS` on a reachable host.

## When previews stop working

- Every preview fails: check outbound 80/443 from the service user and DNS resolution.
- One site never previews: it may answer non-HTML, need more than 3 redirects, be slower than
  3 s, put its title after `<body>`, or resolve to a denied address. That is expected; the
  post simply shows no preview.
- Previews show but never an image: media is disabled (the log says `media disabled` at
  start-up), the image is not one of the accepted types or is over 5 MiB, its host is rate
  limited, or the scan refused it (a match shows up in the moderation queue at priority 0).
- A site shows the fetcher something other than what visitors see: users report it with
  "preview misleading" (moderation queue, P0-W12).
