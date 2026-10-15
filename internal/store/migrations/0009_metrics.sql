-- Phase 0 (SCAFFOLD-0a): aggregate product metrics only (no user IDs, edges, chains or IPs).
CREATE TABLE metrics_daily (
  day              date        NOT NULL,
  name             text        NOT NULL CHECK (name ~ '^[a-z][a-z0-9_.]*$'),
  bucket           text        NOT NULL DEFAULT '',           -- closed label set per name (gate/metrics/names.go)
  value            bigint      NOT NULL DEFAULT 0,
  PRIMARY KEY (day, name, bucket)
);
