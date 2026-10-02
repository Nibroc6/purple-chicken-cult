-- Phase 1 (SCAFFOLD-1): token-only themes (section 15.2 A). hide_via already exists since 0002.
CREATE TABLE user_themes (
  user_id          bigint      PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  tokens           jsonb       NOT NULL,                      -- validated server-side (contrast, size floor, font allowlist)
  updated_at       timestamptz NOT NULL DEFAULT now()
);
