package integration

import (
	"fmt"
	"html"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/khoryik96-creator/Garbage/internal/storage"
	"github.com/khoryik96-creator/Garbage/internal/web"
)

func auditBrowser(t *testing.T) (*storage.Store, browser) {
	t.Helper()
	s, err := storage.Open(filepath.Join(t.TempDir(), "audit.db"))
	must(t, err)
	t.Cleanup(func() { s.Close() })
	handler, err := web.New(s, false)
	must(t, err)
	return s, browser{handler: handler}
}

func TestAuditPaginationPreservesTiesAndTimestampPrecision(t *testing.T) {
	for _, tied := range []bool{true, false} {
		t.Run(fmt.Sprintf("tied=%t", tied), func(t *testing.T) {
			s, b := auditBrowser(t)
			must(t, s.Transaction(func(r *storage.Repository) error {
				for i := 0; i < 105; i++ {
					created := 1000.0
					if !tied {
						created += float64(i) * 1e-12
					}
					if _, err := r.Tx.Exec("INSERT INTO audit_events(id,action,details,created_at) VALUES(?,?,?,?)", fmt.Sprintf("%032x", i), fmt.Sprintf("event_%03d", i), `{}`, created); err != nil {
						return err
					}
				}
				return nil
			}))
			eventPattern := regexp.MustCompile(`(?s)<strong>\s*Event (\d+)\s*</strong>`)
			linkPattern := regexp.MustCompile(`href="(/audit\?before=[^"]+)"`)
			path, previous, total := "/audit", 105, 0
			for page := 0; ; page++ {
				if page > 2 {
					t.Fatal("audit pagination did not finish")
				}
				response := b.request("GET", path, "", false)
				if response.Code != 200 {
					t.Fatal(response.Body.String())
				}
				items := eventPattern.FindAllStringSubmatch(response.Body.String(), -1)
				want := 50
				if page == 2 {
					want = 5
				}
				if len(items) != want {
					t.Fatalf("page %d has %d events, want %d", page, len(items), want)
				}
				for _, item := range items {
					index, err := strconv.Atoi(item[1])
					must(t, err)
					if index != previous-1 {
						t.Fatalf("lost or duplicated event: previous %d, next %d", previous, index)
					}
					previous = index
					total++
				}
				link := linkPattern.FindStringSubmatch(response.Body.String())
				if link == nil {
					break
				}
				path = html.UnescapeString(link[1])
				if !strings.Contains(path, "&before_id=") {
					t.Fatal("audit cursor lacks the event ID")
				}
			}
			if total != 105 {
				t.Fatal("audit pagination hid stored events")
			}
		})
	}
}

func TestAuditCursorValidationAndLegacyLinks(t *testing.T) {
	s, b := auditBrowser(t)
	must(t, s.Transaction(func(r *storage.Repository) error {
		for i := 0; i < 3; i++ {
			if _, err := r.Tx.Exec("INSERT INTO audit_events(id,action,details,created_at) VALUES(?,?,?,?)", fmt.Sprintf("%032x", i), fmt.Sprintf("event_%d", i), `{}`, 999+i); err != nil {
				return err
			}
		}
		return nil
	}))
	for _, path := range []string{
		"/audit?before_id=" + strings.Repeat("0", 32),
		"/audit?before=1000&before_id=bad",
		"/audit?before=1000&before_id=" + strings.Repeat("z", 32),
		"/audit?before=NaN", "/audit?before=Inf", "/audit?before=",
		"/audit?before=1000&before=999",
		"/audit?before=1000&before_id=" + strings.Repeat("0", 32) + "&before_id=" + strings.Repeat("0", 32),
	} {
		if response := b.request("GET", path, "", false); response.Code != 422 {
			t.Fatalf("invalid cursor accepted: %s (%d)", path, response.Code)
		}
	}
	legacy := b.request("GET", "/audit?before=1000", "", false)
	if legacy.Code != 200 || !strings.Contains(legacy.Body.String(), "Event 0") || strings.Contains(legacy.Body.String(), "Event 1") || strings.Contains(legacy.Body.String(), "Event 2") {
		t.Fatal("legacy timestamp cursor changed meaning")
	}
}
