-- Repair an existing Lab 3 volume without deleting its databases or records.
-- Run as postgres against the postgres database; do not use --single-transaction.
\set ON_ERROR_STOP on
\encoding UTF8

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'program') THEN
        CREATE ROLE program LOGIN PASSWORD 'test';
    END IF;
END
$$;

-- CREATE DATABASE must run outside a transaction. Only create missing databases.
SELECT format('CREATE DATABASE %I', required.name)
FROM (VALUES ('reservations'), ('payments'), ('loyalties')) AS required(name)
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = required.name)
\gexec

GRANT ALL PRIVILEGES ON DATABASE reservations, payments, loyalties TO program;

-- These scripts preserve existing tables and seeded records on repeated runs.
\ir ../30-init-v2.sql
\ir ../40-grant-v2.sql
\ir ../50-seed-v2.sql
\ir ../60-loyalty-idempotency.sql
