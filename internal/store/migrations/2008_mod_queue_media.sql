-- Steward (contract request p1-w04-1): a CSAM queue item names the held media,
-- so the queue view and the csam_report action find the csam_holds row. Written
-- by gate/moderation (P1-W06) from core.ModQueueItem.Media; NULL for every other source.
ALTER TABLE mod_queue ADD COLUMN subject_media uuid REFERENCES media(id);
