-- P0-W10: indexes for the unread count and the hourly batch flush (additive).
CREATE INDEX notifications_unread ON notifications (user_id, updated_at DESC, id DESC) WHERE read_at IS NULL;
CREATE INDEX notifications_flush ON notifications (delivered_at) WHERE read_at IS NULL AND group_key IS NOT NULL;
