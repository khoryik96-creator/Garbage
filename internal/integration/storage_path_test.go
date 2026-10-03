package integration

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/storage"
)

func TestDatabasePathsPreserveSpacesUnicodeAndURICharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace 100% #+", "résumé #100%.db")
	store, err := storage.Open(path)
	must(t, err)
	t.Cleanup(func() { store.Close() })
	profile := domain.Candidate{ID: 1, Name: "Saved profile", Notes: "Based in: Malaysia", OtherFields: map[string]string{"email": "sample@example.com"}, Version: 1}
	must(t, store.Transaction(func(r *storage.Repository) error { return r.AddCandidate(profile) }))
	must(t, store.Close())
	if _, err := os.Stat(path); err != nil {
		t.Fatal("database was not created at the exact requested path", err)
	}
	store, err = storage.Open(path)
	must(t, err)
	if saved := candidate(t, store, profile.ID); !reflect.DeepEqual(saved, profile) {
		t.Fatal("reopening the database lost or changed the saved profile")
	}
}
