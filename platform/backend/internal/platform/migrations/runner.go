package migrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const advisoryLockID int64 = 0x504c4154464f524d

var migrationNamePattern = regexp.MustCompile(`^(\d{6})_([a-z0-9][a-z0-9_]*)\.(up|down)\.sql$`)

type Migration struct {
	Version      int64
	Name         string
	UpSQL        string
	DownSQL      string
	UpChecksum   string
	DownChecksum string
}

type appliedMigration struct {
	Name         string
	UpChecksum   string
	DownChecksum string
}

func Load(directory string) ([]Migration, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read migration directory: %w", err)
	}

	byVersion := make(map[int64]*Migration)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := migrationNamePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			if strings.HasSuffix(entry.Name(), ".sql") {
				return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
			}
			continue
		}
		version, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse migration version %q: %w", match[1], err)
		}
		contents, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		if _, err := transactionBody(string(contents)); err != nil {
			return nil, fmt.Errorf("migration %q: %w", entry.Name(), err)
		}
		checksum := sha256.Sum256(contents)
		migration := byVersion[version]
		if migration == nil {
			migration = &Migration{Version: version, Name: match[2]}
			byVersion[version] = migration
		} else if migration.Name != match[2] {
			return nil, fmt.Errorf("migration version %06d has conflicting names %q and %q", version, migration.Name, match[2])
		}
		switch match[3] {
		case "up":
			if migration.UpSQL != "" {
				return nil, fmt.Errorf("migration %06d has duplicate up files", version)
			}
			migration.UpSQL = string(contents)
			migration.UpChecksum = hex.EncodeToString(checksum[:])
		case "down":
			if migration.DownSQL != "" {
				return nil, fmt.Errorf("migration %06d has duplicate down files", version)
			}
			migration.DownSQL = string(contents)
			migration.DownChecksum = hex.EncodeToString(checksum[:])
		}
	}

	versions := make([]int64, 0, len(byVersion))
	for version, migration := range byVersion {
		if migration.UpSQL == "" || migration.DownSQL == "" {
			return nil, fmt.Errorf("migration %06d must have paired up and down files", version)
		}
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	result := make([]Migration, 0, len(versions))
	for _, version := range versions {
		result = append(result, *byVersion[version])
	}
	return result, nil
}

func ApplyUp(ctx context.Context, pool *pgxpool.Pool, directory string) error {
	loaded, err := Load(directory)
	if err != nil {
		return err
	}
	if len(loaded) == 0 {
		return errors.New("no migrations found")
	}
	return withLockedConnection(ctx, pool, func(conn *pgx.Conn) error {
		if err := ensureHistory(ctx, conn); err != nil {
			return err
		}
		applied, err := loadApplied(ctx, conn)
		if err != nil {
			return err
		}
		if err := validateHistory(loaded, applied); err != nil {
			return err
		}
		for _, migration := range loaded {
			if _, exists := applied[migration.Version]; exists {
				continue
			}
			body, _ := transactionBody(migration.UpSQL)
			if err := applyOne(ctx, conn, body, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `INSERT INTO platform_meta.schema_migrations(version,name,up_checksum,down_checksum) VALUES($1,$2,$3,$4)`, migration.Version, migration.Name, migration.UpChecksum, migration.DownChecksum)
				return err
			}); err != nil {
				return fmt.Errorf("apply migration %06d_%s: %w", migration.Version, migration.Name, err)
			}
		}
		return nil
	})
}

// ApplyDownAll is intentionally explicit and is only suitable for disposable
// test databases or a separately reviewed recovery operation.
func ApplyDownAll(ctx context.Context, pool *pgxpool.Pool, directory string) error {
	loaded, err := Load(directory)
	if err != nil {
		return err
	}
	return withLockedConnection(ctx, pool, func(conn *pgx.Conn) error {
		if err := ensureHistory(ctx, conn); err != nil {
			return err
		}
		applied, err := loadApplied(ctx, conn)
		if err != nil {
			return err
		}
		if err := validateHistory(loaded, applied); err != nil {
			return err
		}
		for index := len(loaded) - 1; index >= 0; index-- {
			migration := loaded[index]
			if _, exists := applied[migration.Version]; !exists {
				continue
			}
			body, _ := transactionBody(migration.DownSQL)
			if migration.Version == loaded[0].Version {
				body = "DROP TABLE platform_meta.schema_migrations;\n" + body
			}
			if err := applyOne(ctx, conn, body, func(tx pgx.Tx) error {
				if migration.Version == loaded[0].Version {
					return nil
				}
				_, err := tx.Exec(ctx, `DELETE FROM platform_meta.schema_migrations WHERE version=$1`, migration.Version)
				return err
			}); err != nil {
				return fmt.Errorf("rollback migration %06d_%s: %w", migration.Version, migration.Name, err)
			}
		}
		return nil
	})
}

func withLockedConnection(ctx context.Context, pool *pgxpool.Pool, run func(*pgx.Conn) error) error {
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockID); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() { _, _ = connection.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryLockID) }()
	return run(connection.Conn())
}

func ensureHistory(ctx context.Context, conn *pgx.Conn) error {
	_, err := conn.Exec(ctx, `
		CREATE SCHEMA IF NOT EXISTS platform_meta;
		CREATE TABLE IF NOT EXISTS platform_meta.schema_migrations (
			version BIGINT PRIMARY KEY,
			name TEXT NOT NULL,
			up_checksum TEXT NOT NULL,
			down_checksum TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		return fmt.Errorf("ensure migration history: %w", err)
	}
	return nil
}

func loadApplied(ctx context.Context, conn *pgx.Conn) (map[int64]appliedMigration, error) {
	rows, err := conn.Query(ctx, `SELECT version,name,up_checksum,down_checksum FROM platform_meta.schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read migration history: %w", err)
	}
	defer rows.Close()
	result := make(map[int64]appliedMigration)
	for rows.Next() {
		var version int64
		var migration appliedMigration
		if err := rows.Scan(&version, &migration.Name, &migration.UpChecksum, &migration.DownChecksum); err != nil {
			return nil, fmt.Errorf("scan migration history: %w", err)
		}
		result[version] = migration
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate migration history: %w", err)
	}
	return result, nil
}

func validateHistory(loaded []Migration, applied map[int64]appliedMigration) error {
	loadedByVersion := make(map[int64]Migration, len(loaded))
	seenPending := false
	for _, migration := range loaded {
		loadedByVersion[migration.Version] = migration
		history, exists := applied[migration.Version]
		if !exists {
			seenPending = true
			continue
		}
		if seenPending {
			return fmt.Errorf("migration history has a gap before version %06d", migration.Version)
		}
		if history.Name != migration.Name || history.UpChecksum != migration.UpChecksum || history.DownChecksum != migration.DownChecksum {
			return fmt.Errorf("migration %06d_%s checksum or name differs from applied history", migration.Version, migration.Name)
		}
	}
	for version := range applied {
		if _, exists := loadedByVersion[version]; !exists {
			return fmt.Errorf("applied migration %06d is missing from the repository", version)
		}
	}
	return nil
}

func applyOne(ctx context.Context, conn *pgx.Conn, body string, after func(pgx.Tx) error) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, body); err != nil {
		return err
	}
	if err := after(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func transactionBody(contents string) (string, error) {
	normalized := strings.TrimSpace(strings.ReplaceAll(contents, "\r\n", "\n"))
	lines := strings.Split(normalized, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "BEGIN;" || strings.TrimSpace(lines[len(lines)-1]) != "COMMIT;" {
		return "", errors.New("migration must have a BEGIN;/COMMIT; transaction envelope")
	}
	body := strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
	if body == "" {
		return "", errors.New("migration transaction body is empty")
	}
	return body, nil
}
