#!/usr/bin/env bash
set -e

# PostgreSQL runs this only when the data directory is initially empty.
# SQL files 30, 40, 50 and 60 are then executed by the image entrypoint.
: "${APP_DB_PASSWORD:?APP_DB_PASSWORD must be supplied through postgres-auth}"

psql --username "$POSTGRES_USER" --dbname postgres \
    --set=ON_ERROR_STOP=1 --set=app_password="$APP_DB_PASSWORD" <<'SQL'
CREATE USER program WITH PASSWORD :'app_password';
CREATE DATABASE reservations OWNER program;
CREATE DATABASE payments OWNER program;
CREATE DATABASE loyalties OWNER program;
SQL
