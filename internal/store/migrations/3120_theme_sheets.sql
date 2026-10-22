-- P1-W09 follow-up: advanced themes (contract request p1-w09-3). css is the
-- theme's normalized theme CSS (gate/themes sheet.go: a closed dialect, never
-- raw CSS); theme_images are the theme's own uploads, which image(name) names.
ALTER TABLE user_themes ADD COLUMN css text NOT NULL DEFAULT '';
CREATE TABLE theme_images (
  user_id          bigint      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name             text        NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,31}$'),
  media_id         uuid        NOT NULL UNIQUE REFERENCES media(id),
  PRIMARY KEY (user_id, name)
);
