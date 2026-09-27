#!/bin/sh
set -e

echo "[postgres-init] scanning docker containers for 'sling.postgres.init' labels..."

if [ -n "$COMPOSE_PROJECT_NAME" ]; then
    SERVICES=$(curl -s --unix-socket /var/run/docker.sock "http://localhost/containers/json?all=true" \
      | jq -r --arg proj "$COMPOSE_PROJECT_NAME" '.[] | select(.Labels["com.docker.compose.project"] == $proj) | .Labels["sling.postgres.init"] // empty' | sort -u)
else
    SERVICES=$(curl -s --unix-socket /var/run/docker.sock "http://localhost/containers/json?all=true" \
      | jq -r '.[] | .Labels["sling.postgres.init"] // empty' | sort -u)
fi

if [ -z "$SERVICES" ]; then
    echo "[postgres-init] no databases to initialize."
    exit 0
fi

for srv in $SERVICES; do
    # Validate service identifier to prevent SQL injection or illegal characters
    case "$srv" in
        *[!a-zA-Z0-9_]*)
            echo "[postgres-init] warning: skipping invalid service label '$srv' (only alphanumeric and underscores allowed)"
            continue
            ;;
    esac

    db="${srv}_db"
    user="${srv}_user"
    pass="${srv}_pass"

    echo "[postgres-init] provisioning: db='$db', user='$user'..."

    psql -h "$PGHOST" -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tc \
      "SELECT 1 FROM pg_roles WHERE rolname = '$user'" | grep -q 1 || \
    psql -h "$PGHOST" -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c \
      "CREATE ROLE \"$user\" LOGIN PASSWORD '$pass';"

    psql -h "$PGHOST" -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tc \
      "SELECT 1 FROM pg_database WHERE datname = '$db'" | grep -q 1 || \
    psql -h "$PGHOST" -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c \
      "CREATE DATABASE \"$db\" OWNER \"$user\";"

    psql -h "$PGHOST" -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c \
      "GRANT ALL PRIVILEGES ON DATABASE \"$db\" TO \"$user\";"
done

echo "[postgres-init] all databases provisioned successfully"