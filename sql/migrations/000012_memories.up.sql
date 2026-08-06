-- 000012_memories — what a repository remembers about itself. M5 (FLWL-7).
--
-- The gap it closes (FLWL-71): an agent picking a repository back up had to re-read the whole
-- backlog and the code to find out why things are the way they are. Tasks say WHAT is being done;
-- nothing said WHY it was decided, or what already bit.
--
-- SCOPE: THE REPOSITORY, AND NOTHING ELSE. Read and write by the project's token, no crossing,
-- ever. The team-wide memory was dropped on 2026-08-05 — a shared memory an agent reads as
-- instructions is an injection channel between repositories. Here an agent only ever reads what
-- its own repository wrote, so there is no framing to wire and no trust contract to extend.
--
-- WHY A SLUG AND NOT THE PROJECT COUNTER. Tasks and issues share `projects.next_number`, so
-- CORE-34 names exactly one object. Memories deliberately stay OUT of that namespace: the very
-- registry this feature has to absorb — our `docs/decisions.md` — names its entries D24, D25, D26,
-- and those identifiers are cited across three repositories. Drawing memory identifiers from the
-- reference counter would renumber every one of them and break every citation. A slug is what the
-- author already writes.
--
-- THREE KINDS, NOT NINE, and the reason is a measurement rather than a preference. The reference
-- system Maxence has been running for months has eight typed registers; the ones with entries are
-- those hooked onto a moment that already exists (a decision gets made, an audit ends). The
-- "blockers" register, the only one demanding a write for its own sake, holds nothing but its
-- template after months. A resolved blocker IS a learning, and separating them is what emptied the
-- second one.

CREATE TYPE memory_kind AS ENUM ('decision', 'learning', 'state');

-- Two facts of the project row, so the whole feature stays one table:
--   * memory_bytes bounds the free text agents store here (same reason as note_bytes, 000011);
--   * the FK below is composite, as every tenancy foreign key of this schema is.
ALTER TABLE projects ADD COLUMN memory_bytes bigint NOT NULL DEFAULT 0;

CREATE TABLE memories (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id    uuid        NOT NULL,
    project_id uuid        NOT NULL,
    slug       text        NOT NULL,
    kind       memory_kind NOT NULL,
    title      text        NOT NULL,
    body_md    text        NOT NULL,

    -- superseded_by points at the entry that REPLACED this one. Non-negotiable, and it is the
    -- reason this table exists rather than a `docs/` file: six cards were found stale on
    -- 2026-08-05 because nothing said which decision had overtaken which.
    --
    -- An entry is never edited and never deleted — it is superseded. That is what makes the
    -- history readable: "why is it like this" and "why was it like that" are both answerable.
    superseded_by uuid,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- The search index is a GENERATED column, not a trigger: an index that cannot fall out of
    -- step with its row, because Postgres recomputes it in the same write.
    --
    -- 'english' and not 'simple'. Everything this repository publishes is English (see
    -- .claude/rules/code-conventions.md), so stemming pays off — "decision" finds "decisions".
    -- A French entry stays searchable: words the English dictionary does not know pass through
    -- unstemmed rather than being dropped. Postgres detects no language, so the alternative was
    -- 'simple', which costs stemming everywhere to gain nothing anywhere.
    --
    -- The title is weighted A and the body B: a search that names an entry's subject must surface
    -- that entry before the ones merely mentioning it.
    search tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('english', title), 'A') ||
        setweight(to_tsvector('english', body_md), 'B')
    ) STORED,

    CONSTRAINT memories_project_fk FOREIGN KEY (project_id, team_id)
        REFERENCES projects (id, team_id) ON DELETE CASCADE,

    -- A memory can only ever be superseded by a memory of the SAME PROJECT. The key is composite
    -- for the same reason every tenancy key here is: without it, nothing at the schema level would
    -- stop an entry from pointing at a sibling's, and the pointer would then leak that sibling's
    -- identifier into a repository that must not know it exists.
    CONSTRAINT memories_supersedes_fk FOREIGN KEY (superseded_by, project_id)
        REFERENCES memories (id, project_id),

    CONSTRAINT memories_slug_unique_per_project UNIQUE (project_id, slug),
    -- The pair addressed by the composite key above.
    CONSTRAINT memories_id_project_unique UNIQUE (id, project_id),

    -- A slug is cited from elsewhere — a commit, a card, another entry — so it has to survive
    -- being written by hand: letters, digits, dash and underscore, nothing that needs escaping in
    -- a URL or a shell.
    CONSTRAINT memories_slug_shape CHECK (slug ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$'),
    CONSTRAINT memories_title_not_blank CHECK (btrim(title) <> ''),
    CONSTRAINT memories_title_length CHECK (char_length(title) <= 200),
    CONSTRAINT memories_body_not_blank CHECK (btrim(body_md) <> ''),
    -- An entry cannot supersede itself: the cycle of length one is the only one a single insert
    -- can create, and it would make the "still in force" read loop.
    CONSTRAINT memories_no_self_supersede CHECK (superseded_by IS NULL OR superseded_by <> id)
);

-- The nominal read: the entries of a project still in force, most recent first. Partial on
-- superseded_by IS NULL, because that is what every read but the history one asks for.
CREATE INDEX memories_project_live_idx
    ON memories (project_id, kind, created_at DESC)
    WHERE superseded_by IS NULL;

-- Full-text search, scoped by project. GIN because the column is a tsvector and reads vastly
-- outnumber writes here.
CREATE INDEX memories_search_idx ON memories USING gin (search);
