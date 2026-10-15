-- Phase 0 (SCAFFOLD-0a): posts, versions, parent links, mentions, link previews.
CREATE TABLE posts (
  id               uuid        PRIMARY KEY,
  author_id        bigint      NOT NULL REFERENCES users(id),
  body             text,                                      -- NFC-normalized at write; NULL after delete (hard delete)
  body_salt        bytea,                                     -- 16 random bytes per version; NULL after delete (security #25)
  audience         smallint    CHECK (audience IN (1, 2, 3, 10)), -- NULL everyone connected; 10 = friend+ only (P1)
  audience_forced  boolean     NOT NULL DEFAULT false,        -- set because author was on probation; permanent (D6)
  ref_policy       smallint    NOT NULL DEFAULT 0,            -- 0 anyone, 1 two_hops, 2 friends, 3 nobody (slippery)
  lang             text        NOT NULL DEFAULT 'und',
  no_preview       boolean     NOT NULL DEFAULT false,
  version          int         NOT NULL DEFAULT 1,
  created_at       timestamptz NOT NULL,
  edited_at        timestamptz,
  deleted_at       timestamptz,
  record_hash      bytea       NOT NULL,                      -- latest version's record
  CHECK (char_length(body) <= 20000)
);
CREATE INDEX posts_author_time ON posts (author_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX posts_time ON posts (created_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE post_versions (
  post_id          uuid        NOT NULL REFERENCES posts(id),
  version          int         NOT NULL,
  record_hash      bytea       NOT NULL,                      -- the version's record signs body_commit = sha256(salt || utf8(body))
  created_at       timestamptz NOT NULL,
  PRIMARY KEY (post_id, version)
);

CREATE TABLE post_parents (
  child_id         uuid        NOT NULL REFERENCES posts(id),
  parent_id        uuid        NOT NULL REFERENCES posts(id),
  ord              smallint    NOT NULL CHECK (ord BETWEEN 0 AND 2),
  kind             smallint    NOT NULL CHECK (kind IN (0, 1)),   -- 0 ref, 1 cont
  child_author     bigint      NOT NULL,
  parent_author    bigint      NOT NULL,
  detached         boolean     NOT NULL DEFAULT false,
  shadow           boolean     NOT NULL DEFAULT false,
  created_at       timestamptz NOT NULL,
  PRIMARY KEY (child_id, parent_id)
);
CREATE INDEX post_parents_parent ON post_parents (parent_id, created_at DESC) INCLUDE (child_id, child_author, kind, detached, shadow);
CREATE UNIQUE INDEX post_parents_one_cont ON post_parents (parent_id) WHERE kind = 1;
CREATE INDEX post_parents_refcap ON post_parents (child_author, parent_id, created_at);

CREATE TABLE mentions (
  post_id          uuid        NOT NULL REFERENCES posts(id),
  ord              smallint    NOT NULL,                      -- index of the @handle occurrence in body
  target_id        bigint      NOT NULL REFERENCES users(id),
  PRIMARY KEY (post_id, ord)
);
CREATE INDEX mentions_target ON mentions (target_id);

CREATE TABLE link_previews (
  post_id          uuid        NOT NULL REFERENCES posts(id),
  ord              smallint    NOT NULL,
  url_canon        text        NOT NULL,                      -- tracking params stripped
  site_host        text        NOT NULL,                      -- ASCII (punycode) host: the trusted field
  title            text,
  description      text        CHECK (char_length(description) <= 200),
  status           smallint    NOT NULL DEFAULT 0,            -- 0 pending, 1 ok, 2 failed, 3 skipped
  fetched_at       timestamptz,
  ready_at         timestamptz NOT NULL,                      -- created_at + k*PreviewDelay, k in {1,2}; never shown before.
                                                              -- A fetch not finished by bucket 1 moves to bucket 2 once;
                                                              -- not finished by bucket 2 => status 2
  PRIMARY KEY (post_id, ord)
);

CREATE TABLE link_fetch_cache (                                -- internal, TTL ~1h (section 10.1.6)
  url_canon        text        PRIMARY KEY,
  status           smallint    NOT NULL,
  title            text,
  description      text,
  site_host        text        NOT NULL,
  fetched_at       timestamptz NOT NULL,
  expires_at       timestamptz NOT NULL
);
