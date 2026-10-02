package jobs

import (
	"context"
	"errors"
	"log"
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
	var claim *domain.Claim
	err := w.Store.Transaction(func(r *storage.Repository) error {
		var err error
		claim, err = r.Claim(w.Clock(), w.LeaseSeconds)
		return err
	})
	if err != nil || claim == nil {
		return false, err
	}
	err = w.ProcessClaim(*claim)
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
	var run domain.Run
	var page domain.CandidatePage
	err := w.Store.Transaction(func(r *storage.Repository) error {
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
		return err
	}
	// Document/model calls happen outside database transactions. Results remain bounded
	// by one page and are committed together only while this claim still owns its lease.
	results := make([]*domain.Extraction, len(page.Candidates))
	missing, proposed, existing, notFound := 0, 0, 0, 0
	for index, c := range page.Candidates {
		if !domain.Empty(c.Country) {
			existing++
			continue
		}
		missing++
		result, err := w.Extractor.ExtractCountry(c)
		if err != nil {
			return err
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
	return w.Store.Transaction(func(r *storage.Repository) error {
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
}
func (w *Worker) Run(ctx context.Context) {
	timer := time.NewTicker(250 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if _, err := w.ProcessOne(); err != nil {
				log.Print("Queue unavailable; retrying.")
			}
		}
	}
}
