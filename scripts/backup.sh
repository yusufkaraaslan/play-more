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

# SQLite online backup. The .backup command takes a consistent snapshot
# that already incorporates any in-flight WAL contents — the resulting
# .db file is fully self-contained and can be restored standalone. Do NOT
# also copy the live WAL/SHM files: writes between the .backup snapshot
# and the cp would yield a backup whose .db is consistent but whose
# accompanying WAL is stale, causing replay corruption on restore.
sqlite3 "$DB_FILE" ".backup '$BACKUP_FILE'"

# Encrypt if GPG recipient is configured
if [ -n "$GPG_RECIPIENT" ] && command -v gpg &> /dev/null; then
    gpg --batch --yes --recipient "$GPG_RECIPIENT" --encrypt --output "$BACKUP_FILE.gpg" "$BACKUP_FILE"
    rm -f "$BACKUP_FILE"
    echo "Encrypted backup created: $BACKUP_FILE.gpg"
else
    echo "Backup created: $BACKUP_FILE"
fi

# Rotate old backups
find "$BACKUP_DIR" -name 'playmore_*.db*' -type f -mtime +$RETENTION_DAYS -delete

echo "Backup complete. Retention: $RETENTION_DAYS days."
