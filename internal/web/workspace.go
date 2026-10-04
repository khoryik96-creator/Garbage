package web

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/khoryik96-creator/Garbage/internal/domain"
)

const maxRestoreBytes int64 = 512 << 20

func (a *App) backupWorkspace(w http.ResponseWriter, r *http.Request) {
	if err := formKeys(r, "_csrf"); err != nil {
		a.fail(w, r, err, 0)
		return
	}
	path := filepath.Join(filepath.Dir(a.Store.Path), ".download-"+domain.ID()+".db")
	defer os.Remove(path)
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if err := a.Store.Backup(ctx, path); err != nil {
		a.fail(w, r, domain.Invalid("The backup could not be created. Your workspace has been kept. Try again."), 0)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\"Garbage-Truck-backup-"+time.Now().UTC().Format("20060102-150405")+".db\"")
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	http.ServeFile(w, r, path)
}

func (a *App) restoreWorkspace(w http.ResponseWriter, r *http.Request) {
	if err := formKeys(r, "_csrf"); err != nil {
		a.fail(w, r, err, 0)
		return
	}
	if r.MultipartForm == nil || len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["backup"]) != 1 {
		a.fail(w, r, domain.Invalid("Choose one Garbage Truck backup file."), 0)
		return
	}
	upload, header, err := r.FormFile("backup")
	if err != nil {
		a.fail(w, r, domain.Invalid("Choose a backup file."), 0)
		return
	}
	defer upload.Close()
	if header.Size == 0 || header.Size > maxRestoreBytes {
		a.fail(w, r, domain.Invalid("Choose a backup file smaller than 512 MiB."), 0)
		return
	}
	file, err := os.CreateTemp(filepath.Dir(a.Store.Path), ".restore-*.db")
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	path := file.Name()
	defer func() { _ = os.Remove(path); _ = os.Remove(path + "-wal"); _ = os.Remove(path + "-shm") }()
	_, err = io.Copy(file, upload)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		a.fail(w, r, domain.Invalid("The backup could not be read. Your current workspace has been kept."), 0)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if _, err = a.Store.Restore(ctx, path); err != nil {
		a.fail(w, r, err, 0)
		return
	}
	http.Redirect(w, r, "/settings?restored=1", http.StatusSeeOther)
}
