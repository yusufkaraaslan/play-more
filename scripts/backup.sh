#!/bin/bash
# PlayMore SQLite backup script
# Run nightly via cron: 0 2 * * * /path/to/playmore/scripts/backup.sh /app/data
#
# Creates timestamped backups with rotation (keeps last 7 days).
# Backups are encrypted with gpg if GPG_RECIPIENT is set.

set -euo pipefail

DATA_DIR="${1:-./data}"
BACKUP_DIR="${DATA_DIR}/backups"
RETENTION_DAYS=7
GPG_RECIPIENT="${GPG_RECIPIENT:-}"

mkdir -p "$BACKUP_DIR"

DB_FILE="$DATA_DIR/playmore.db"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="$BACKUP_DIR/playmore_$TIMESTAMP.db"

if [ ! -f "$DB_FILE" ]; then
    echo "Database not found at $DB_FILE"
    exit 1
fi

# SQLite online backup (copies the DB safely even while in use)
sqlite3 "$DB_FILE" ".backup '$BACKUP_FILE'"

# Also backup WAL/shm if they exist
if [ -f "$DATA_DIR/playmore.db-wal" ]; then
    cp "$DATA_DIR/playmore.db-wal" "$BACKUP_DIR/playmore_$TIMESTAMP.db-wal"
fi
if [ -f "$DATA_DIR/playmore.db-shm" ]; then
    cp "$DATA_DIR/playmore.db-shm" "$BACKUP_DIR/playmore_$TIMESTAMP.db-shm"
fi

# Encrypt if GPG recipient is configured
if [ -n "$GPG_RECIPIENT" ] && command -v gpg &> /dev/null; then
    gpg --batch --yes --recipient "$GPG_RECIPIENT" --encrypt --output "$BACKUP_FILE.gpg" "$BACKUP_FILE"
    rm -f "$BACKUP_FILE"
    [ -f "$BACKUP_DIR/playmore_$TIMESTAMP.db-wal" ] && rm -f "$BACKUP_DIR/playmore_$TIMESTAMP.db-wal"
    [ -f "$BACKUP_DIR/playmore_$TIMESTAMP.db-shm" ] && rm -f "$BACKUP_DIR/playmore_$TIMESTAMP.db-shm"
    echo "Encrypted backup created: $BACKUP_FILE.gpg"
else
    echo "Backup created: $BACKUP_FILE"
fi

# Rotate old backups
find "$BACKUP_DIR" -name 'playmore_*.db*' -type f -mtime +$RETENTION_DAYS -delete

echo "Backup complete. Retention: $RETENTION_DAYS days."
