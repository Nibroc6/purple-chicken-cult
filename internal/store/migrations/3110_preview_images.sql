-- P1-W11: preview images. The cache keeps the page's og:image URL (canonical,
-- tracking parameters stripped); the image itself is fetched and ingested once
-- per post, so every post owns its own media row (media.source_post).
ALTER TABLE link_fetch_cache ADD COLUMN image_url text;
