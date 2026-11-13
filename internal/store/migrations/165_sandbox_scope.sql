-- A light sandbox copies the whole store except its products: the team
-- brings the products it wants to try (and can send them to another
-- sandbox). Much less disk than a full copy.
ALTER TABLE sandboxes ADD COLUMN scope text NOT NULL DEFAULT 'full' CHECK (scope IN ('full', 'light'));
