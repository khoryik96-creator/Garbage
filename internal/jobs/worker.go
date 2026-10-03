package jobs

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/khoryik96-creator/Garbage/internal/connectors"
	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/pipeline"
	"github.com/khoryik96-creator/Garbage/internal/policy"
	"github.com/khoryik96-creator/Garbage/internal/storage"
)

type Worker struct {
	Store        *storage.Store
	PageSize     int
	LeaseSeconds float64
	MaxAttempts  int
	Clock        func() float64
	Extractor    domain.Extractor
	Gateway      func(*storage.Repository) domain.CandidateGateway
}

func New(s *storage.Store, pageSize int) *Worker {
	return &Worker{Store: s, PageSize: pageSize, LeaseSeconds: 60, MaxAttempts: 5, Clock: storage.Now, Extractor: pipeline.RuleCountryExtractor{}, Gateway: connectors.Factory}
}
func (w *Worker) ProcessOne() (bool, error) {
	return w.ProcessOneContext(context.Background())
}
func (w *Worker) ProcessOneContext(ctx context.Context) (bool, error) {
	var claim *domain.Claim
	err := w.Store.TransactionContext(ctx, func(r *storage.Repository) error {
		var err error
		claim, err = r.Claim(w.Clock(), w.LeaseSeconds, w.MaxAttempts)
		return err
	})
	if err != nil || claim == nil {
		return false, err
	}
	err = w.ProcessClaimContext(ctx, *claim)
	if ctx.Err() != nil {
		return true, w.Store.Transaction(func(r *storage.Repository) error { return r.Release(*claim) })
	}
	if err == nil || errors.Is(err, domain.ErrLease) {
		return true, nil
	}
	// Provider errors may contain profile data: persist and log only a fixed message.
	log.Print("Page processing failed; retry scheduled.")
	retryErr := w.Store.Transaction(func(r *storage.Repository) error { return r.Retry(*claim, w.Clock(), w.MaxAttempts) })
	if errors.Is(retryErr, domain.ErrLease) {
		retryErr = nil
	}
	return true, retryErr
}
func (w *Worker) ProcessClaim(claim domain.Claim) error {
	return w.ProcessClaimContext(context.Background(), claim)
}
func (w *Worker) ProcessClaimContext(parent context.Context, claim domain.Claim) error {
	ctx, cancel := context.WithCancelCause(parent)
	renew := func() error {
		return w.Store.TransactionContext(ctx, func(r *storage.Repository) error {
			return r.Renew(claim, w.Clock(), w.LeaseSeconds)
		})
	}
	// Poll run ownership during a request as well as between profiles. Losing the
	// token (pause/cancel/reclaim) cancels in-flight document HTTP requests promptly.
	interval := min(time.Duration(w.LeaseSeconds*float64(time.Second)/3), 250*time.Millisecond)
	if interval <= 0 {
		cancel(domain.ErrLease)
		return domain.ErrLease
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := renew(); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	stopHeartbeat := sync.OnceFunc(func() { close(stop); <-done })
	defer func() { cancel(nil); stopHeartbeat() }()
	pageErr := func(err error) error {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		return err
	}
	var run domain.Run
	var page domain.CandidatePage
	err := w.Store.TransactionContext(ctx, func(r *storage.Repository) error {
		if err := r.Owned(claim, w.Clock()); err != nil {
			return err
		}
		var err error
		run, err = r.Run(claim.RunID)
		if err != nil {
			return err
		}
		if run.State != "queued" && run.State != "running" {
			return domain.ErrLease
		}
		gateway := w.Gateway(r)
		page, err = gateway.Page(run.Cursor, run.UpperBound, w.PageSize)
		return err
	})
	if err != nil {
		return pageErr(err)
	}
	// Document/model calls happen outside database transactions. Results remain bounded
	// by one page and are committed together only while this claim still owns its lease.
	results := make([]*domain.Extraction, len(page.Candidates))
	missing, proposed, existing, notFound := 0, 0, 0, 0
	for index, c := range page.Candidates {
		if err := renew(); err != nil {
			return pageErr(err)
		}
		if !domain.Empty(c.Country) {
			existing++
			continue
		}
		missing++
		result, err := w.Extractor.ExtractCountry(ctx, c)
		if err != nil {
			return pageErr(err)
		}
		if result == nil {
			notFound++
			continue
		}
		if err = policy.ValidateEvidence(c, *result); err != nil {
			return err
		}
		results[index] = result
		proposed++
	}
	// Join the heartbeat before committing: it must not mistake a completed job
	// for lost ownership and cancel the transaction that completed it.
	stopHeartbeat()
	err = w.Store.TransactionContext(ctx, func(r *storage.Repository) error {
		if err := r.Owned(claim, w.Clock()); err != nil {
			return err
		}
		current, err := r.Run(claim.RunID)
		if err != nil {
			return err
		}
		if (current.State != "queued" && current.State != "running") || current.Cursor != run.Cursor {
			return domain.ErrLease
		}
		for index, result := range results {
			if result != nil {
				if err := r.AddSuggestion(run, page.Candidates[index], *result); err != nil {
					return err
				}
			}
		}
		return r.Checkpoint(claim, w.Clock(), page, missing, proposed, existing, notFound)
	})
	return pageErr(err)
}
func (w *Worker) Run(ctx context.Context) {
	timer := time.NewTicker(250 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if _, err := w.ProcessOneContext(ctx); err != nil && ctx.Err() == nil {
				log.Print("Queue unavailable; retrying.")
			}
		}
	}
}
