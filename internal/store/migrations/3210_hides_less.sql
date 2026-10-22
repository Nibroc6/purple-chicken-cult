-- Trial feedback: hides and show-less can be undone. A hide remembers whether
-- it came from "show less" (and so bumped show_less for the author), so
-- undoing it takes that step back too.
ALTER TABLE hides ADD COLUMN less boolean NOT NULL DEFAULT false;
