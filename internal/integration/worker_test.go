package integration

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/khoryik96-creator/Garbage/internal/docworker"
	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/pipeline"
	"github.com/khoryik96-creator/Garbage/internal/storage"
)

type contextExtractor func(context.Context, domain.Candidate) (*domain.Extraction, error)

func (f contextExtractor) ExtractCountry(ctx context.Context, c domain.Candidate) (*domain.Extraction, error) {
	return f(ctx, c)
}

func TestSlowPageRenewsBetweenProfiles(t *testing.T) {
	s, w, _ := setup(t)
	w.PageSize = 100
	var now atomic.Int64
	now.Store(100)
	w.Clock = func() float64 { return float64(now.Load()) }
	w.Extractor = contextExtractor(func(ctx context.Context, c domain.Candidate) (*domain.Extraction, error) {
		now.Add(10)
		return (pipeline.RuleCountryExtractor{}).ExtractCountry(ctx, c)
	})
	run := newRun(t, s, "review")
	work, err := w.ProcessOne()
	must(t, err)
	current := getRun(t, s, run.ID)
	if !work || now.Load() != 180 || current.State != "completed" || current.Processed != 10 || current.Proposed != 6 {
		t.Fatalf("slow page did not finish: %+v", current)
	}
}

func TestHeartbeatRenewsDuringExtraction(t *testing.T) {
	s, w, _ := setup(t)
	w.PageSize, w.LeaseSeconds = 100, 0.6
	var calls atomic.Int64
	w.Extractor = contextExtractor(func(ctx context.Context, c domain.Candidate) (*domain.Extraction, error) {
		if calls.Add(1) == 1 {
			timer := time.NewTimer(1400 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		return (pipeline.RuleCountryExtractor{}).ExtractCountry(ctx, c)
	})
	run := newRun(t, s, "preview")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := w.ProcessOneContext(ctx)
	must(t, err)
	if current := getRun(t, s, run.ID); current.State != "completed" || current.Processed != 10 {
		t.Fatalf("heartbeat lost a healthy long request: %+v", current)
	}
}

func TestAbandonedLeasesHaveBoundedRetries(t *testing.T) {
	s, w, _ := setup(t)
	w.MaxAttempts = 2
	run := newRun(t, s, "review")
	var first, second *domain.Claim
	must(t, s.Transaction(func(r *storage.Repository) error {
		var err error
		first, err = r.Claim(100, 60, w.MaxAttempts)
		return err
	}))
	w.Clock = func() float64 { return 161 }
	if work, err := w.ProcessOne(); err != nil || work {
		t.Fatal("expired attempt did not enter backoff", err)
	}
	must(t, s.Transaction(func(r *storage.Repository) error {
		if err := r.Renew(*first, 161, 60); !errors.Is(err, domain.ErrLease) {
			t.Fatal("expired token was renewed", err)
		}
		var err error
		second, err = r.Claim(163, 60, w.MaxAttempts)
		return err
	}))
	if second == nil || second.Attempt != 2 || second.Token == first.Token {
		t.Fatal("recovery did not count and fence the abandoned attempt")
	}
	w.Clock = func() float64 { return 224 }
	for i := 0; i < 3; i++ {
		if work, err := w.ProcessOne(); err != nil || work {
			t.Fatal("failed job was reclaimed", err)
		}
	}
	failed := getRun(t, s, run.ID)
	if failed.State != "failed" || failed.Processed != 0 || failed.Error == nil {
		t.Fatalf("abandoned job exceeded attempt limit: %+v", failed)
	}
	must(t, s.Transaction(func(r *storage.Repository) error {
		var attempts, audits int
		if err := r.Tx.QueryRow("SELECT attempts FROM jobs WHERE id=?", run.ID).Scan(&attempts); err != nil {
			return err
		}
		if err := r.Tx.QueryRow("SELECT COUNT(*) FROM audit_events WHERE run_id=? AND action='run_failed'", run.ID).Scan(&audits); err != nil {
			return err
		}
		if attempts != 2 || audits != 1 {
			t.Fatal("attempt cap or failure audit was duplicated")
		}
		_, err := r.ChangeRun(run.ID, "resume")
		return err
	}))
	drain(t, w)
	if getRun(t, s, run.ID).State != "completed" {
		t.Fatal("failed abandoned job could not resume")
	}
}

func TestRunControlStopsBeforeNextProfile(t *testing.T) {
	for _, action := range []string{"pause", "cancel"} {
		t.Run(action, func(t *testing.T) {
			s, w, _ := setup(t)
			w.PageSize = 100
			run := newRun(t, s, "review")
			calls := 0
			w.Extractor = contextExtractor(func(ctx context.Context, c domain.Candidate) (*domain.Extraction, error) {
				calls++
				must(t, s.Transaction(func(r *storage.Repository) error { _, err := r.ChangeRun(run.ID, action); return err }))
				return (pipeline.RuleCountryExtractor{}).ExtractCountry(ctx, c)
			})
			_, err := w.ProcessOne()
			must(t, err)
			if calls != 1 || getRun(t, s, run.ID).Processed != 0 {
				t.Fatal("stopped run continued extracting or committed a partial page")
			}
		})
	}
}

func TestRunControlCancelsInFlightDocumentRequest(t *testing.T) {
	for _, action := range []string{"pause", "cancel", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			s, w, _ := setup(t)
			w.PageSize = 100
			run := newRun(t, s, "review")
			entered, disconnected := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				_, _ = io.Copy(io.Discard, request.Body)
				close(entered)
				<-request.Context().Done()
				close(disconnected)
			}))
			defer server.Close()
			client, err := docworker.New(server.URL)
			must(t, err)
			w.Extractor = client
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			finished := make(chan error, 1)
			go func() { _, err := w.ProcessOneContext(ctx); finished <- err }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("document request did not start")
			}
			if action == "shutdown" {
				cancel()
			} else {
				must(t, s.Transaction(func(r *storage.Repository) error { _, err := r.ChangeRun(run.ID, action); return err }))
			}
			select {
			case err := <-finished:
				must(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("worker did not stop an active HTTP request")
			}
			select {
			case <-disconnected:
			case <-time.After(time.Second):
				t.Fatal("document HTTP request was left running")
			}
			if getRun(t, s, run.ID).Processed != 0 {
				t.Fatal("interrupted page committed progress")
			}
			must(t, s.Transaction(func(r *storage.Repository) error {
				items, err := r.Suggestions(run.ID, "", 100)
				if len(items) != 0 {
					t.Fatal("interrupted page committed proposals")
				}
				return err
			}))
			if action == "cancel" {
				if getRun(t, s, run.ID).State != "cancelled" {
					t.Fatal("cancelled run changed state")
				}
				return
			}
			if action == "pause" {
				must(t, s.Transaction(func(r *storage.Repository) error { _, err := r.ChangeRun(run.ID, "resume"); return err }))
			} else {
				must(t, s.Transaction(func(r *storage.Repository) error {
					var state string
					var attempts int
					err := r.Tx.QueryRow("SELECT state,attempts FROM jobs WHERE id=?", run.ID).Scan(&state, &attempts)
					if state != "queued" || attempts != 0 {
						t.Fatal("shutdown consumed an attempt or stranded its lease")
					}
					return err
				}))
			}
			w.Extractor = pipeline.RuleCountryExtractor{}
			drain(t, w)
			if current := getRun(t, s, run.ID); current.State != "completed" || current.Processed != 10 || current.Proposed != 6 {
				t.Fatalf("resume lost or duplicated page results: %+v", current)
			}
		})
	}
}

func TestWorkerRunCancelsAnActivePage(t *testing.T) {
	s, w, _ := setup(t)
	run := newRun(t, s, "review")
	entered, finished := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	w.Extractor = contextExtractor(func(ctx context.Context, c domain.Candidate) (*domain.Extraction, error) {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { defer close(finished); w.Run(ctx) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("worker shutdown waited on the rest of its page")
	}
	if calls.Load() != 1 || getRun(t, s, run.ID).Processed != 0 {
		t.Fatal("shutdown continued page extraction")
	}
}
