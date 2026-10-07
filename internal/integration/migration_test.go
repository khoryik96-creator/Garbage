package integration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/khoryik96-creator/Garbage/internal/audit"
	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/storage"
)

func TestLegacyWorkspaceAndBackupPreserveHistoryAndUndo(t *testing.T) {
	s, w, _ := setup(t)
	run := completed(t, s, w, "review")
	approved, err := audit.New(s).Approve(proposal(t, s, run.ID, 1001).ID, domain.Approval{})
	must(t, err)
	legacy := filepath.Join(t.TempDir(), "legacy.db")
	must(t, s.Backup(context.Background(), legacy))
	old, err := sql.Open("sqlite3", legacy)
	must(t, err)
	_, err = old.Exec("ALTER TABLE runs DROP COLUMN protection; ALTER TABLE writebacks DROP COLUMN field; ALTER TABLE writebacks DROP COLUMN guard_version; DELETE FROM schema_migrations WHERE version=2;")
	must(t, err)
	must(t, old.Close())
	original, err := os.ReadFile(legacy)
	must(t, err)

	// Restore upgrades a staging copy, without altering the user's older backup.
	_, err = s.Restore(context.Background(), legacy)
	must(t, err)
	unchanged, err := os.ReadFile(legacy)
	must(t, err)
	if sha256.Sum256(original) != sha256.Sum256(unchanged) {
		t.Fatal("restore modified the legacy backup")
	}
	if getRun(t, s, run.ID).Protection != "fill_blanks" {
		t.Fatal("legacy preservation rule missing")
	}
	state, err := audit.New(s).Undo(*approved.WritebackID)
	must(t, err)
	if state != "undone" || !domain.Empty(candidate(t, s, 1001).Country) {
		t.Fatal("legacy approval history cannot be undone")
	}

	// Startup also upgrades an existing workspace, retaining the old approval.
	upgraded, err := storage.Open(legacy)
	must(t, err)
	defer upgraded.Close()
	if getRun(t, upgraded, run.ID).State != "completed" || candidate(t, upgraded, 1001).Country == nil {
		t.Fatal("upgrade lost saved data")
	}
	state, err = audit.New(upgraded).Undo(*approved.WritebackID)
	must(t, err)
	if state != "undone" {
		t.Fatal("upgraded history cannot be undone")
	}
}
