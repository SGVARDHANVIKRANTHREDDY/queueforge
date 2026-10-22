-- Least-privilege roles for QueueForge (closes THREAT_MODEL gap T14).
--
-- Run migrations first, then run this ONCE as a superuser, e.g.:
--   goose -dir migrations postgres "$SUPERUSER_URL" up
--   psql "$SUPERUSER_URL" -v app_pw="'...'" -v migrate_pw="'...'" -f deploy/sql/least_privilege.sql
--
-- Result:
--   qf_migrate : owns the schema, runs migrations only   -> QF_MIGRATE_DATABASE_URL
--   qf_app     : DML only (no CREATE/DROP/ALTER/TRUNCATE) -> QF_DATABASE_URL (API, workers)
-- A SQL-injection or compromised app process can then no longer drop tables or alter constraints.
-- NOTE: audit_log is append-only by trigger; qf_app additionally gets no UPDATE/DELETE on it.
\set ON_ERROR_STOP on

DO $$
BEGIN
  IF to_regclass('public.audit_log') IS NULL THEN
    RAISE EXCEPTION 'QueueForge migrations have not run: public.audit_log is missing';
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'qf_migrate') THEN CREATE ROLE qf_migrate LOGIN; END IF;
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'qf_app')     THEN CREATE ROLE qf_app LOGIN;     END IF;
END $$;
ALTER ROLE qf_migrate PASSWORD :migrate_pw;
ALTER ROLE qf_app     PASSWORD :app_pw;

-- hand the schema to the migration role
DO $$
DECLARE r record;
BEGIN
  EXECUTE format('ALTER SCHEMA public OWNER TO qf_migrate');
  FOR r IN SELECT tablename FROM pg_tables WHERE schemaname = 'public' LOOP
    EXECUTE format('ALTER TABLE public.%I OWNER TO qf_migrate', r.tablename);
  END LOOP;
  FOR r IN SELECT sequencename FROM pg_sequences WHERE schemaname = 'public' LOOP
    EXECUTE format('ALTER SEQUENCE public.%I OWNER TO qf_migrate', r.sequencename);
  END LOOP;
END $$;

REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO qf_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO qf_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO qf_app;
REVOKE UPDATE, DELETE, TRUNCATE ON public.audit_log FROM qf_app;

-- tables created by FUTURE migrations (run as qf_migrate) get the same grants automatically
ALTER DEFAULT PRIVILEGES FOR ROLE qf_migrate IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO qf_app;
ALTER DEFAULT PRIVILEGES FOR ROLE qf_migrate IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO qf_app;
