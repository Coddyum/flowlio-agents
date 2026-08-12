-- Drop the tier column and its CHECK. The constraint goes with the column, but naming it here keeps
-- the down migration explicit about everything the up added.
ALTER TABLE issues DROP CONSTRAINT IF EXISTS issues_effort_values;
ALTER TABLE issues DROP COLUMN IF EXISTS effort;
