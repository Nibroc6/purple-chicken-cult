-- Phase 0 (SCAFFOLD-0a): likes/saves, seen state, hides, show-less.
CREATE TABLE engagements (
  user_id          bigint      NOT NULL REFERENCES users(id),
  post_id          uuid        NOT NULL REFERENCES posts(id),
  kind             smallint    NOT NULL,                      -- 1 like, 2 save (P1)
  at               timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, post_id, kind)
);
CREATE INDEX engagements_post ON engagements (post_id, kind);

CREATE TABLE seen (                                            -- 14-day retention, nightly purge
  user_id          bigint      NOT NULL,
  post_id          uuid        NOT NULL,
  at               timestamptz NOT NULL,
  PRIMARY KEY (user_id, post_id)
);
CREATE INDEX seen_user_at ON seen (user_id, at DESC);
CREATE INDEX seen_at ON seen (at);

CREATE TABLE hides (
  user_id          bigint      NOT NULL,
  post_id          uuid        NOT NULL,
  at               timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, post_id)
);
CREATE TABLE show_less (
  user_id          bigint      NOT NULL,
  author_id        bigint      NOT NULL,
  n                int         NOT NULL DEFAULT 1,
  last_at          timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, author_id)
);
