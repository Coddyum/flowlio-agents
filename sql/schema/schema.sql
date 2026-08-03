--
-- PostgreSQL database dump
--


-- Dumped from database version 18.4
-- Dumped by pg_dump version 18.4

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: event_subject; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.event_subject AS ENUM (
    'task',
    'issue'
);


--
-- Name: issue_state; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.issue_state AS ENUM (
    'open',
    'answered',
    'closed'
);


--
-- Name: task_priority; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.task_priority AS ENUM (
    'low',
    'normal',
    'high',
    'urgent'
);


--
-- Name: task_status; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.task_status AS ENUM (
    'todo',
    'in_progress',
    'blocked',
    'done'
);


--
-- Name: token_scope; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.token_scope AS ENUM (
    'admin',
    'project'
);


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.events (
    id bigint NOT NULL,
    team_id uuid NOT NULL,
    project_id uuid NOT NULL,
    actor_project_id uuid NOT NULL,
    kind text NOT NULL,
    subject_type public.event_subject NOT NULL,
    subject_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT events_kind_format CHECK ((kind ~ '^[a-z_]+\.[a-z_]+$'::text))
);


--
-- Name: events_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.events ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: issue_messages; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.issue_messages (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    issue_id uuid NOT NULL,
    author_project_id uuid NOT NULL,
    body_md text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT issue_messages_body_not_blank CHECK ((btrim(body_md) <> ''::text))
);


--
-- Name: issues; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.issues (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    team_id uuid NOT NULL,
    project_id uuid NOT NULL,
    author_project_id uuid NOT NULL,
    number bigint NOT NULL,
    title text NOT NULL,
    state public.issue_state DEFAULT 'open'::public.issue_state NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    closed_at timestamp with time zone,
    CONSTRAINT issues_closed_at_shape CHECK (((state = 'closed'::public.issue_state) = (closed_at IS NOT NULL))),
    CONSTRAINT issues_not_self CHECK ((author_project_id <> project_id)),
    CONSTRAINT issues_number_positive CHECK ((number >= 1)),
    CONSTRAINT issues_title_length CHECK ((char_length(title) <= 200)),
    CONSTRAINT issues_title_not_blank CHECK ((btrim(title) <> ''::text))
);


--
-- Name: project_trust; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.project_trust (
    team_id uuid NOT NULL,
    low_project_id uuid NOT NULL,
    high_project_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT project_trust_ordered CHECK ((low_project_id < high_project_id))
);


--
-- Name: projects; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.projects (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    team_id uuid NOT NULL,
    key text NOT NULL,
    name text NOT NULL,
    next_number bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT projects_key_format CHECK ((key ~ '^[A-Z][A-Z0-9]{1,9}$'::text)),
    CONSTRAINT projects_name_not_blank CHECK ((btrim(name) <> ''::text)),
    CONSTRAINT projects_next_number_positive CHECK ((next_number >= 1))
);


--
-- Name: schema_migrations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.schema_migrations (
    version bigint NOT NULL,
    dirty boolean NOT NULL
);


--
-- Name: task_notes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.task_notes (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    task_id uuid NOT NULL,
    body_md text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT task_notes_body_not_blank CHECK ((btrim(body_md) <> ''::text))
);


--
-- Name: tasks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tasks (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    team_id uuid NOT NULL,
    project_id uuid NOT NULL,
    number bigint NOT NULL,
    title text NOT NULL,
    body_md text DEFAULT ''::text NOT NULL,
    status public.task_status DEFAULT 'todo'::public.task_status NOT NULL,
    priority public.task_priority DEFAULT 'normal'::public.task_priority NOT NULL,
    deadline timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    archived_at timestamp with time zone,
    CONSTRAINT tasks_deadline_bounded CHECK (((deadline IS NULL) OR (deadline < '9999-01-01 00:00:00+00'::timestamp with time zone))),
    CONSTRAINT tasks_number_positive CHECK ((number >= 1)),
    CONSTRAINT tasks_title_length CHECK ((char_length(title) <= 200)),
    CONSTRAINT tasks_title_not_blank CHECK ((btrim(title) <> ''::text))
);


--
-- Name: teams; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.teams (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    slug text NOT NULL,
    name text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT teams_name_not_blank CHECK ((btrim(name) <> ''::text)),
    CONSTRAINT teams_slug_format CHECK ((slug ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$'::text))
);


--
-- Name: token_cursors; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.token_cursors (
    token_id uuid NOT NULL,
    last_event_id bigint DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT token_cursors_last_event_id_positive CHECK ((last_event_id >= 0))
);


--
-- Name: tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tokens (
    id uuid DEFAULT gen_random_uuid() CONSTRAINT agent_tokens_id_not_null NOT NULL,
    team_id uuid,
    project_id uuid,
    name text CONSTRAINT agent_tokens_name_not_null NOT NULL,
    prefix text CONSTRAINT agent_tokens_prefix_not_null NOT NULL,
    secret_hash text CONSTRAINT agent_tokens_secret_hash_not_null NOT NULL,
    created_at timestamp with time zone DEFAULT now() CONSTRAINT agent_tokens_created_at_not_null NOT NULL,
    last_used_at timestamp with time zone,
    revoked_at timestamp with time zone,
    scope public.token_scope NOT NULL,
    CONSTRAINT agent_tokens_name_not_blank CHECK ((btrim(name) <> ''::text)),
    CONSTRAINT agent_tokens_prefix_format CHECK ((prefix ~ '^[a-z0-9]{12}$'::text)),
    CONSTRAINT tokens_scope_shape CHECK ((((scope = 'project'::public.token_scope) AND (team_id IS NOT NULL) AND (project_id IS NOT NULL)) OR ((scope = 'admin'::public.token_scope) AND (team_id IS NULL) AND (project_id IS NULL))))
);


--
-- Name: events events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_pkey PRIMARY KEY (id);


--
-- Name: issue_messages issue_messages_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.issue_messages
    ADD CONSTRAINT issue_messages_pkey PRIMARY KEY (id);


--
-- Name: issues issues_number_unique_per_project; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.issues
    ADD CONSTRAINT issues_number_unique_per_project UNIQUE (project_id, number);


--
-- Name: issues issues_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.issues
    ADD CONSTRAINT issues_pkey PRIMARY KEY (id);


--
-- Name: project_trust project_trust_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_trust
    ADD CONSTRAINT project_trust_pkey PRIMARY KEY (team_id, low_project_id, high_project_id);


--
-- Name: projects projects_id_team_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_id_team_unique UNIQUE (id, team_id);


--
-- Name: projects projects_key_unique_per_team; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_key_unique_per_team UNIQUE (team_id, key);


--
-- Name: projects projects_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_pkey PRIMARY KEY (id);


--
-- Name: schema_migrations schema_migrations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.schema_migrations
    ADD CONSTRAINT schema_migrations_pkey PRIMARY KEY (version);


--
-- Name: task_notes task_notes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_notes
    ADD CONSTRAINT task_notes_pkey PRIMARY KEY (id);


--
-- Name: tasks tasks_number_unique_per_project; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_number_unique_per_project UNIQUE (project_id, number);


--
-- Name: tasks tasks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_pkey PRIMARY KEY (id);


--
-- Name: teams teams_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.teams
    ADD CONSTRAINT teams_pkey PRIMARY KEY (id);


--
-- Name: teams teams_slug_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.teams
    ADD CONSTRAINT teams_slug_key UNIQUE (slug);


--
-- Name: token_cursors token_cursors_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.token_cursors
    ADD CONSTRAINT token_cursors_pkey PRIMARY KEY (token_id);


--
-- Name: tokens tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tokens
    ADD CONSTRAINT tokens_pkey PRIMARY KEY (id);


--
-- Name: tokens tokens_prefix_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tokens
    ADD CONSTRAINT tokens_prefix_key UNIQUE (prefix);


--
-- Name: events_actor_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_actor_idx ON public.events USING btree (actor_project_id);


--
-- Name: events_subject_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_subject_idx ON public.events USING btree (subject_id, id);


--
-- Name: events_team_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_team_idx ON public.events USING btree (team_id, id);


--
-- Name: issue_messages_author_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX issue_messages_author_idx ON public.issue_messages USING btree (author_project_id);


--
-- Name: issue_messages_thread_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX issue_messages_thread_idx ON public.issue_messages USING btree (issue_id, created_at, id);


--
-- Name: issues_incoming_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX issues_incoming_idx ON public.issues USING btree (project_id, team_id, state, updated_at DESC);


--
-- Name: issues_outgoing_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX issues_outgoing_idx ON public.issues USING btree (author_project_id, team_id, state, updated_at DESC);


--
-- Name: project_trust_high_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX project_trust_high_idx ON public.project_trust USING btree (high_project_id, team_id);


--
-- Name: projects_team_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX projects_team_id_idx ON public.projects USING btree (team_id);


--
-- Name: task_notes_task_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX task_notes_task_id_idx ON public.task_notes USING btree (task_id, created_at);


--
-- Name: tasks_project_active_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX tasks_project_active_idx ON public.tasks USING btree (project_id, status, number DESC) WHERE (archived_at IS NULL);


--
-- Name: tokens_project_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX tokens_project_id_idx ON public.tokens USING btree (project_id);


--
-- Name: tokens agent_tokens_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tokens
    ADD CONSTRAINT agent_tokens_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE CASCADE;


--
-- Name: tokens agent_tokens_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tokens
    ADD CONSTRAINT agent_tokens_team_id_fkey FOREIGN KEY (team_id) REFERENCES public.teams(id) ON DELETE CASCADE;


--
-- Name: events events_actor_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_actor_fk FOREIGN KEY (actor_project_id, team_id) REFERENCES public.projects(id, team_id) ON DELETE CASCADE;


--
-- Name: events events_project_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_project_fk FOREIGN KEY (project_id, team_id) REFERENCES public.projects(id, team_id) ON DELETE CASCADE;


--
-- Name: issue_messages issue_messages_author_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.issue_messages
    ADD CONSTRAINT issue_messages_author_project_id_fkey FOREIGN KEY (author_project_id) REFERENCES public.projects(id) ON DELETE CASCADE;


--
-- Name: issue_messages issue_messages_issue_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.issue_messages
    ADD CONSTRAINT issue_messages_issue_id_fkey FOREIGN KEY (issue_id) REFERENCES public.issues(id) ON DELETE CASCADE;


--
-- Name: issues issues_author_project_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.issues
    ADD CONSTRAINT issues_author_project_fk FOREIGN KEY (author_project_id, team_id) REFERENCES public.projects(id, team_id) ON DELETE CASCADE;


--
-- Name: issues issues_project_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.issues
    ADD CONSTRAINT issues_project_fk FOREIGN KEY (project_id, team_id) REFERENCES public.projects(id, team_id) ON DELETE CASCADE;


--
-- Name: project_trust project_trust_high_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_trust
    ADD CONSTRAINT project_trust_high_fk FOREIGN KEY (high_project_id, team_id) REFERENCES public.projects(id, team_id) ON DELETE CASCADE;


--
-- Name: project_trust project_trust_low_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_trust
    ADD CONSTRAINT project_trust_low_fk FOREIGN KEY (low_project_id, team_id) REFERENCES public.projects(id, team_id) ON DELETE CASCADE;


--
-- Name: projects projects_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_team_id_fkey FOREIGN KEY (team_id) REFERENCES public.teams(id) ON DELETE CASCADE;


--
-- Name: task_notes task_notes_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_notes
    ADD CONSTRAINT task_notes_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE CASCADE;


--
-- Name: tasks tasks_project_fk; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_project_fk FOREIGN KEY (project_id, team_id) REFERENCES public.projects(id, team_id) ON DELETE CASCADE;


--
-- Name: token_cursors token_cursors_token_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.token_cursors
    ADD CONSTRAINT token_cursors_token_id_fkey FOREIGN KEY (token_id) REFERENCES public.tokens(id) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--


