-- Phase 0 (SCAFFOLD-0a): Postgres job queue (store/jobq).
CREATE TABLE jobs (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  kind             text        NOT NULL,
  payload          jsonb       NOT NULL DEFAULT '{}'::jsonb,
  dedupe_key       text,                                      -- optional idempotency key
  run_at           timestamptz NOT NULL DEFAULT now(),
  attempts         int         NOT NULL DEFAULT 0,
  max_attempts     int         NOT NULL DEFAULT 5,
  locked_until     timestamptz,
  last_error       text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  done_at          timestamptz
);
CREATE INDEX jobs_ready ON jobs (run_at) WHERE done_at IS NULL;
CREATE UNIQUE INDEX jobs_dedupe ON jobs (kind, dedupe_key) WHERE done_at IS NULL AND dedupe_key IS NOT NULL;
