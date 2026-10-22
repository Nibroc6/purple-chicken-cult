-- P1-W04: media pipeline state beyond the SCAFFOLD-1 tables (2003_media.sql).
ALTER TABLE media
  ADD COLUMN attached_at timestamptz,             -- set by core.Media.Attach in the post's transaction (single use)
  ADD COLUMN purged_at   timestamptz;             -- variants' blobs deleted and their CDN keys purged (media.process)
ALTER TABLE csam_holds
  ADD COLUMN account_released_at timestamptz,     -- ReleaseAccount (false positive): the subject may upload again
  ADD COLUMN evidence_purged_at  timestamptz;     -- evidence object deleted after retain_until (media.process)

-- the account hold: does this user have an unreleased hold?
CREATE INDEX csam_holds_subject ON csam_holds (subject_user) WHERE account_released_at IS NULL;
-- media.process: deleted media whose blobs are not purged yet; unattached uploads to sweep
CREATE INDEX media_purge_pending ON media (deleted_at) WHERE deleted_at IS NOT NULL AND purged_at IS NULL;
CREATE INDEX media_unattached ON media (created_at) WHERE attached_at IS NULL AND deleted_at IS NULL AND owner_id IS NOT NULL;
CREATE INDEX media_owner ON media (owner_id) WHERE deleted_at IS NULL;
CREATE INDEX media_source_post ON media (source_post) WHERE source_post IS NOT NULL;
