#!/usr/bin/env bash
set -e

# Create the databases for the Hotels Booking System (v2).
export VARIANT="v2"
export SCRIPT_PATH=/docker-entrypoint-initdb.d/
export PGPASSWORD=postgres
psql -U postgres -d postgres -v ON_ERROR_STOP=1 -f "$SCRIPT_PATH/scripts/db-$VARIANT.sql"
