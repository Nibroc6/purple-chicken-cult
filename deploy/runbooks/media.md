# Media: stores, scanner, signed URLs and the media Worker

Owner: P1-W04. Applies to every environment that stores images (ARCHITECTURE §6, §17.3;
plan §4.7, D20).

## Fail closed

Media is on only when `cfg.MediaEnabled()` is true. In prod that needs **all** of:

| Setting | Value |
|---|---|
| `PCC_SCANNER` | `photodna`, with `PCC_PHOTODNA_KEY` and `PCC_PHOTODNA_URL` (the Match endpoint, e.g. `https://api.microsoftmoderator.com/photodna/v1.0/Match`) |
| `PCC_BLOB_DRIVER` | `s3` (`PCC_S3_ENDPOINT`, `PCC_S3_BUCKET`, `PCC_S3_ACCESS_KEY`, `PCC_S3_SECRET_KEY`, `PCC_S3_REGION=auto` for R2), or `fs` + `PCC_ALLOW_FS_BLOB=1` (single host, logged at WARN on every start) |
| `PCC_EVIDENCE_DRIVER` | a **separate** store (`PCC_EVIDENCE_S3_*`), see csam.md |
| `PCC_MEDIA_BASE_URL` | the media origin, e.g. `https://media.pcc.example/m` |
| `PCC_MASTER_KEY` | present (the URL key is derived from it) |

Anything missing, or `PCC_SCANNER=dev|none`, keeps the server running with media off:
uploads answer `409 {"error":"feature_disabled","feature":"media"}`, preview images are
skipped, and the start log has `media disabled` at ERROR. `PCC_SCANNER=cloudflare` (or any
other value) refuses to start: Cloudflare's CSAM tool scans after storage. The `dev`
scanner (SHA-256 list in `testdata/scan/blocklist.txt`, synthetic test patterns only)
exists only in dev and test.

A scanner error or timeout (15 s) rejects the upload (`media_rejected`) and stores
nothing. If PhotoDNA is down, uploads fail; nothing is queued for a later scan.

## Pipeline

1. Header check (`image.DecodeConfig`): JPEG, PNG, WebP or GIF; ≤ 12,000 px per side and
   ≤ 40 MP, ≤ 15 MiB. Bombs are refused before any pixel buffer exists.
2. PhotoDNA Match (WebP and sides under 160 px are converted to PNG for matching only).
3. Match ⇒ csam.md. Clean ⇒ decode (EXIF orientation applied), WebP thumb 320, display 1080
   and original ≤ 4096 px; re-encoding drops all metadata (EXIF, GPS, XMP). At most two
   images are processed at once.
4. Blob keys are 128 random bits in base32 (26 chars), never content hashes.

## R2 bucket

- One **private** bucket for media (`pcc-media`). **Public access (r2.dev) stays off** and
  no custom domain is attached to the bucket itself: only the Worker reads it.
- The app's S3 token: Object Read & Write on `pcc-media` only.
- The evidence bucket is a different bucket with a different token (csam.md).

## Signed URLs and the Worker

`GET /api/media/{ref}/url?variant=…` checks that the viewer owns the image or can see a post
carrying it **at request time**, then signs:

```
<PCC_MEDIA_BASE_URL>/<key>?e=<unix>&v=<base64url(viewer tag)>&s=<base64url(sig)>
sig = HMAC-SHA256(K, key (26 B ascii) ‖ e (8 B big-endian) ‖ v (6 B))
```

URLs live 50–60 min (expiry rounded down to 10 min so browsers can cache them); the client
asks for a new one on a 403. `original` charges the `media_full` budget (60/day).

`deploy/cloudflare/media-worker.js` serves the media host:

- verifies the HMAC and the expiry **before** the cache lookup (403 otherwise);
- caches by path only (`/m/<key>`; `e`, `v`, `s` are not in the cache key) for at most 1 h;
- sends `X-Content-Type-Options: nosniff`, `Content-Security-Policy: sandbox; default-src
  'none'`, `Content-Disposition: inline`, `Cross-Origin-Resource-Policy` (`CORP`, default
  `same-site`: put the media host under the app's registrable domain), `Referrer-Policy:
  no-referrer`, `X-Robots-Tag`, `tdm-reservation: 1`.

Deploy: copy `wrangler.toml.example`, set the route and bucket, then
`wrangler secret put MEDIA_URL_KEY` with K in hex. K is `HKDF-SHA256(PCC_MASTER_KEY, salt
empty, info "pcc/media-url/v1", 32 bytes)`:

```
openssl kdf -keylen 32 -kdfopt digest:SHA256 -kdfopt hexkey:$PCC_MASTER_KEY \
  -kdfopt info:pcc/media-url/v1 HKDF | tr -d : | tr A-F a-f
```

Rotating `PCC_MASTER_KEY` rotates K: update the secret in the same change. Check the Worker
with `node --test deploy/cloudflare/media-worker.test.mjs` (vectors shared with the Go
signer). In dev and test the Go server serves `/m/<key>` with the same checks; prod
registers no `/m/` route.

## Deletion and CDN purge

Deleting a post or an account, or leaving an upload unattached for 24 h, marks its media
deleted (unservable at once). The `media.process` job (every 10 min, and right after each
deletion) deletes the blobs and purges `<PCC_MEDIA_BASE_URL>/<key>` from the edge with
`PCC_CDN_PURGE=cloudflare`, `PCC_CDN_ZONE_ID`, `PCC_CDN_API_TOKEN` (token permission: Zone ›
Cache Purge on the media zone only). Without purge configured, prod logs a WARN at start and
a deleted image can stay in the edge cache up to its 1 h TTL for holders of a still-valid URL.
Failed purges are retried by the job queue.
