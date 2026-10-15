-- Phase 1 (SCAFFOLD-1): media pipeline and CSAM preservation.
CREATE TABLE media (
  id               uuid        PRIMARY KEY,
  owner_id         bigint      REFERENCES users(id),          -- NULL = system ingest (preview image)
  source_post      uuid,                                      -- preview images: the post whose link produced it
  status           smallint    NOT NULL,                      -- 0 received, 1 ready, 2 rejected, 3 held (CSAM match; evidence preserved)
  kind             smallint    NOT NULL DEFAULT 0,            -- 0 image (video is Phase 2)
  width            int,
  height           int,
  alt              text        CHECK (char_length(alt) <= 1000),
  scan_driver      text        NOT NULL,
  scanned_at       timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  deleted_at       timestamptz
);
CREATE TABLE media_variants (
  media_id         uuid        NOT NULL REFERENCES media(id),
  variant          smallint    NOT NULL,                      -- 0 thumb (320), 1 display (1080), 2 original (re-encoded, metadata stripped)
  blob_key         text        NOT NULL UNIQUE,               -- 128-bit random, base32; never a content hash
  mime             text        NOT NULL,
  width            int         NOT NULL,
  height           int         NOT NULL,
  bytes            int         NOT NULL,
  PRIMARY KEY (media_id, variant)
);
CREATE TABLE post_media (
  post_id          uuid        NOT NULL REFERENCES posts(id),
  ord              smallint    NOT NULL CHECK (ord BETWEEN 0 AND 3),
  media_id         uuid        NOT NULL UNIQUE REFERENCES media(id),
  PRIMARY KEY (post_id, ord)
);
CREATE TABLE csam_holds (                                      -- legal preservation; content access = break-glass only
  media_id         uuid        PRIMARY KEY REFERENCES media(id),
  evidence_key     text        NOT NULL,                      -- key in the separate evidence blob store (1-year retention)
  match_info       jsonb       NOT NULL,                      -- scanner verdict metadata only; this is what moderators see
  source_url       text,                                      -- preview images: canonical URL that served the image
  subject_user     bigint,                                    -- uploader, or author of the post that linked it
  held_at          timestamptz NOT NULL DEFAULT now(),
  retain_until     timestamptz NOT NULL,                      -- held_at + 1 year (REPORT Act 2024 preservation)
  reported_at      timestamptz,
  report_ref       text                                       -- filled by the csam_report moderator action
);
ALTER TABLE link_previews ADD COLUMN image_media_id uuid REFERENCES media(id);
