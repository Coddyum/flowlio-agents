-- 000016_issue_effort — the rigour tier an issue's author declares, so the receiver's waker can pick
-- a model and clamp it (FLWL-84).
--
-- Expand only, and nullable on purpose: the hosted engine is pinned per image (D29) and lags, and an
-- engine that predates this column writes no `effort`. A NULL therefore means "unspecified" — the
-- probe and the waker fold it to the standard tier, exactly what an author who declares nothing gets.
-- A NOT NULL here would be the same production break as 000014 (see 000015). No default, no rewrite:
-- adding a nullable column with no default is instantaneous on Postgres, and the CHECK admits the
-- existing all-NULL rows.
--
-- The four values match internal/pkg/effort verbatim; that package's Valid is the useful error, this
-- CHECK is the guarantee.
ALTER TABLE issues ADD COLUMN effort text
    CONSTRAINT issues_effort_values CHECK (effort IS NULL OR effort IN ('low', 'standard', 'high', 'max'));
