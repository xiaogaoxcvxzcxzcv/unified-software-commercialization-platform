package migrations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRequiresPairedStrictlyNamedTransactionalMigrations(t *testing.T) {
	directory := t.TempDir()
	writeMigration(t, directory, "000001_first.up.sql", "BEGIN;\nSELECT 1;\nCOMMIT;\n")
	writeMigration(t, directory, "000001_first.down.sql", "BEGIN;\nSELECT 2;\nCOMMIT;\n")

	loaded, err := Load(directory)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded) != 1 || loaded[0].Version != 1 || loaded[0].Name != "first" {
		t.Fatalf("Load() = %#v", loaded)
	}
	if loaded[0].UpChecksum == "" || loaded[0].DownChecksum == "" || loaded[0].UpChecksum == loaded[0].DownChecksum {
		t.Fatalf("unexpected checksums: %#v", loaded[0])
	}
}

func TestLoadRejectsMissingPairAndInvalidEnvelope(t *testing.T) {
	t.Run("missing down", func(t *testing.T) {
		directory := t.TempDir()
		writeMigration(t, directory, "000001_first.up.sql", "BEGIN;\nSELECT 1;\nCOMMIT;\n")
		if _, err := Load(directory); err == nil || !strings.Contains(err.Error(), "paired") {
			t.Fatalf("Load() error = %v", err)
		}
	})
	t.Run("missing transaction envelope", func(t *testing.T) {
		directory := t.TempDir()
		writeMigration(t, directory, "000001_first.up.sql", "SELECT 1;")
		writeMigration(t, directory, "000001_first.down.sql", "BEGIN;\nSELECT 2;\nCOMMIT;\n")
		if _, err := Load(directory); err == nil || !strings.Contains(err.Error(), "transaction envelope") {
			t.Fatalf("Load() error = %v", err)
		}
	})
}

func TestValidateHistoryRejectsMutationAndGaps(t *testing.T) {
	loaded := []Migration{
		{Version: 1, Name: "first", UpChecksum: "up-1", DownChecksum: "down-1"},
		{Version: 2, Name: "second", UpChecksum: "up-2", DownChecksum: "down-2"},
	}
	if err := validateHistory(loaded, map[int64]appliedMigration{1: {Name: "first", UpChecksum: "changed", DownChecksum: "down-1"}}); err == nil {
		t.Fatal("validateHistory() accepted a changed checksum")
	}
	if err := validateHistory(loaded, map[int64]appliedMigration{2: {Name: "second", UpChecksum: "up-2", DownChecksum: "down-2"}}); err == nil {
		t.Fatal("validateHistory() accepted a history gap")
	}
}

func writeMigration(t *testing.T, directory, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
