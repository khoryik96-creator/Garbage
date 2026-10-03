package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/khoryik96-creator/Garbage/internal/domain"
)

type Settings struct {
	DatabasePath      string
	EmbeddedWorker    bool
	PageSize          int
	DocumentWorkerURL string
}

func Load() (Settings, error) {
	s := Settings{DatabasePath: ".data/autocoder.db", EmbeddedWorker: true, PageSize: 100, DocumentWorkerURL: os.Getenv("AUTOCODER_DOCUMENT_WORKER_URL")}
	if path := os.Getenv("AUTOCODER_DATABASE_PATH"); path != "" {
		s.DatabasePath = path
	} else if legacy := os.Getenv("AUTOCODER_DATABASE_URL"); legacy != "" {
		if !strings.HasPrefix(legacy, "sqlite:///") {
			return s, domain.Invalid("Only SQLite is supported in this prototype.")
		}
		s.DatabasePath = strings.TrimPrefix(legacy, "sqlite:///")
	}
	if raw := os.Getenv("AUTOCODER_EMBEDDED_WORKER"); raw != "" {
		if raw != "0" && raw != "1" {
			return s, domain.Invalid("Embedded worker must be 0 or 1.")
		}
		s.EmbeddedWorker = raw == "1"
	}
	if raw := os.Getenv("AUTOCODER_PAGE_SIZE"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 1000 {
			return s, domain.Invalid("Page size must be between 1 and 1000.")
		}
		s.PageSize = n
	}
	return s, nil
}
