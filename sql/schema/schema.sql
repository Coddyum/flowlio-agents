--
-- PostgreSQL database dump
--

\restrict dx6OiRpJrM1ZwjbSeg1T8718P4dIbOdn8UzL4aEF6FP9qpQwTj2nuDlep3g9ZAR

-- Dumped from database version 17.10
-- Dumped by pg_dump version 17.10

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
-- Name: token_scope; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.token_scope AS ENUM (
    'admin',
    'project'
);


SET default_tablespace = '';

SET default_table_access_method = heap;

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
-- Name: tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    team_id uuid,
    project_id uuid,
    name text NOT NULL,
    prefix text NOT NULL,
    secret_hash text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    last_used_at timestamp with time zone,
    revoked_at timestamp with time zone,
    scope public.token_scope NOT NULL,
    CONSTRAINT agent_tokens_name_not_blank CHECK ((btrim(name) <> ''::text)),
    CONSTRAINT agent_tokens_prefix_format CHECK ((prefix ~ '^[a-z0-9]{12}$'::text)),
    CONSTRAINT tokens_scope_shape CHECK ((((scope = 'project'::public.token_scope) AND (team_id IS NOT NULL) AND (project_id IS NOT NULL)) OR ((scope = 'admin'::public.token_scope) AND (project_id IS NULL))))
);


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
-- Name: projects_team_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX projects_team_id_idx ON public.projects USING btree (team_id);


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
-- Name: projects projects_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_team_id_fkey FOREIGN KEY (team_id) REFERENCES public.teams(id) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--

\unrestrict dx6OiRpJrM1ZwjbSeg1T8718P4dIbOdn8UzL4aEF6FP9qpQwTj2nuDlep3g9ZAR

