-- Notes on dock fingers (e.g. "flotation poor", "needs replacement").
-- Editable from the harbor map admin view.

ALTER TABLE dock_fingers
    ADD COLUMN notes TEXT NOT NULL DEFAULT '';
