# CSAM: matches, preservation, NCMEC reporting

Owner: P1-W04 (pipeline, holds, evidence) with P1-W06 (moderation queue, `csam_report`
action). ARCHITECTURE §6.1, §13 compliance; plan §4.7; security critique #7.

This runbook is for the operator's legal contact and the moderators who handle CSAM
items. It does not replace legal advice: confirm the current statutory text (18 U.S.C.
§ 2258A as amended by the REPORT Act 2024) with counsel before launch.

## What happens on a match

A PhotoDNA match on an upload **or** on a link-preview image (never dropped silently):

1. The exact bytes go to the **evidence store** (`PCC_EVIDENCE_*`), never to the media store.
   Nothing is served from it, ever.
2. One transaction writes `media` (status 3 = held, no variants), `csam_holds` (scanner
   match metadata, source URL for preview images, subject user, `held_at`,
   `retain_until = held_at + 1 year`) and a **priority 0** `mod_queue` item (source csam)
   naming the held media.
3. The **account is held**: the uploader (or, for a preview image, the author of the post
   that linked it) gets `media_rejected` for every later upload until two moderators
   release it after review. The uploader sees the same `media_rejected` as for any refused
   image; nothing tells them about a match.
4. If the evidence store write fails, the hold and the queue item are still written with
   `match_info.evidence = "unavailable"`, and an ERROR is logged: treat that as an incident
   (the material could not be preserved; record why in the report).

## Matches found outside the app (unscanned mode)

With `PCC_SCANNER=unscanned` (media.md) the steps above never run on their own. Detection
comes from Cloudflare's CSAM Scanning Tool email (matched paths, already blocked at the
edge) or a user report. A match is actual knowledge under § 2258A, so on the day it arrives:

1. Map each path (`/m/<key>`) to its `media` row and uploader. Do not open the image.
2. Preserve: copy the object from the media bucket into the evidence bucket under break-glass
   (two people, logged, as below) and record the key, then delete it from the media bucket.
3. Report to the CyberTipline as below, with the Cloudflare notice time and path in place of
   the PhotoDNA details, and hold or suspend the account (two-person moderation actions).
4. Keep the Cloudflare email with the incident log for at least a year.

## Who sees what

- **Moderators** see the queue item and `match_info` (driver, PhotoDNA tracking id, match
  sources and violation codes), source URL, times and the report state. They **never** see
  the image: the queue view has no media URL and the app has no route to evidence.
- **Nobody** browses the evidence bucket in normal operation. Access is break-glass only.

## Reporting to NCMEC (CyberTipline)

For every CSAM item, as soon as reasonably possible:

1. A moderator claims the item, reviews the metadata, and files a CyberTipline report at
   <https://report.cybertip.org/> (ESP account) with: the incident time (`held_at`), the
   PhotoDNA tracking id and match details, the uploader's account identifiers available to
   the operator, and for preview images the source URL. The image itself is uploaded only
   through the break-glass procedure below if NCMEC asks for it.
2. They enter the CyberTipline report id in the item's `csam_report` action
   (`POST /api/mod/items/{id}/csam-report {report_ref}`); a second moderator confirms
   (two-person). This fills `csam_holds.report_ref` and `reported_at` once and extends
   `retain_until` to at least one year after the report.
3. Remove the related content and act on the account per policy (suspension is a separate
   two-person moderation action). A reviewed false positive may release the upload hold
   (`ReleaseAccount`); the evidence stays until its retention ends and the report decision
   is documented in the item's resolution.

## Preservation (1 year)

- The evidence bucket is separate from the media bucket, private, with its own token
  (Object Read & Write on that bucket only, held by the app) and an R2 **bucket lock /
  retention rule of at least 1 year** so objects cannot be deleted early by anyone.
- `media.process` deletes an evidence object only when the hold **has been reported** and
  `retain_until` has passed. Unreported evidence is never deleted automatically: report it.
- A law-enforcement preservation request extends retention: under break-glass, set
  `retain_until` for that `media_id` to the requested date (record the request id).
- The `csam_holds` row (metadata, report reference) stays after the evidence is deleted.

## Break-glass access to evidence

Only for a request from NCMEC or law enforcement, or counsel's instruction:

1. Two named people (operator + legal contact, or two admins) agree and record the reason,
   the request reference and the time in the incident log **before** access.
2. One person uses a separate, time-limited read token for the evidence bucket (created
   for this access and revoked right after), on an operator machine, never a personal one.
3. The object (`csam_holds.evidence_key`) is transferred only through the requester's
   official channel; no copies are kept. Nobody views it unless the request requires it.
4. The token is revoked and the log entry is completed with what was transferred.

## Contacts and checks

- Keep the legal contact and the CyberTipline ESP registration current (operator).
- Quarterly: confirm the evidence bucket's retention rule, that the app's evidence token
  cannot list or read other buckets, and that no open CSAM queue item is older than a week.
