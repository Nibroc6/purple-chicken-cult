-- Trial feedback: build-up write limits (limits.Policy.Refills). One row per
-- user and kind holds the GCRA "theoretical arrival time": the bucket is full
-- once tat <= now, each charge moves tat on by the refill interval, and a
-- charge is refused while tat would end up more than Cap intervals ahead.
CREATE TABLE rate_buckets (
  user_id          bigint      NOT NULL,
  bucket           text        NOT NULL,                      -- 'b:<write kind>'
  tat              timestamptz NOT NULL,
  PRIMARY KEY (user_id, bucket)
);
CREATE INDEX rate_buckets_tat ON rate_buckets (tat);
