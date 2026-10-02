package storage

import (
	"database/sql"
	"errors"
	"math"

	"github.com/khoryik96-creator/Garbage/internal/domain"
)

func (r *Repository) Claim(now, lease float64) (*domain.Claim, error) {
	var c domain.Claim
	err := r.Tx.QueryRow("SELECT j.id,j.attempts FROM jobs j JOIN runs r ON r.id=j.id WHERE r.state IN ('queued','running') AND ((j.state='queued' AND j.available_at<=?) OR (j.state='leased' AND j.lease_until<=?)) ORDER BY j.available_at,j.id LIMIT 1", now, now).Scan(&c.RunID, &c.Attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.Token = domain.ID()
	c.Attempt++
	err = changed(r.Tx.Exec("UPDATE jobs SET state='leased',token=?,lease_until=?,attempts=attempts+1 WHERE id=? AND ((state='queued' AND available_at<=?) OR (state='leased' AND lease_until<=?))", c.Token, now+lease, c.RunID, now, now))
	if err != nil {
		return nil, err
	}
	return &c, nil
}
func (r *Repository) Owned(c domain.Claim, now float64) error {
	var n int
	err := r.Tx.QueryRow("SELECT COUNT(*) FROM jobs WHERE id=? AND token=? AND state='leased' AND lease_until>?", c.RunID, c.Token, now).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 {
		return domain.ErrLease
	}
	return nil
}
func (r *Repository) Checkpoint(c domain.Claim, now float64, p domain.CandidatePage, missing, proposed, existing, notFound int) error {
	if err := r.Owned(c, now); err != nil {
		return err
	}
	runState, jobState := "running", "queued"
	if p.Finished {
		runState, jobState = "completed", "done"
	}
	result, err := r.Tx.Exec("UPDATE jobs SET state=?,token=NULL,lease_until=NULL,attempts=0,available_at=0,error=NULL WHERE id=? AND token=? AND state='leased' AND lease_until>?", jobState, c.RunID, c.Token, now)
	if err = changed(result, err); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return domain.ErrLease
		}
		return err
	}
	if _, err = r.Tx.Exec("UPDATE runs SET state=?,cursor=?,processed=processed+?,missing=missing+?,proposed=proposed+?,existing=existing+?,not_found=not_found+? WHERE id=?", runState, p.Cursor, len(p.Candidates), missing, proposed, existing, notFound, c.RunID); err != nil {
		return err
	}
	if p.Finished {
		return r.Audit("run_completed", &c.RunID, nil, nil)
	}
	return nil
}
func (r *Repository) Retry(c domain.Claim, now float64, maxAttempts int) error {
	if err := r.Owned(c, now); err != nil {
		return err
	}
	state := "queued"
	message := "Page processing failed; correct the adapter and resume."
	if c.Attempt >= maxAttempts {
		state = "failed"
	}
	delay := math.Min(60, math.Pow(2, float64(min(c.Attempt, 6))))
	if err := changed(r.Tx.Exec("UPDATE jobs SET state=?,token=NULL,lease_until=NULL,available_at=?,error=? WHERE id=? AND token=? AND lease_until>?", state, now+delay, message, c.RunID, c.Token, now)); err != nil {
		return err
	}
	if state == "failed" {
		if _, err := r.Tx.Exec("UPDATE runs SET state='failed',error=? WHERE id=?", message, c.RunID); err != nil {
			return err
		}
		return r.Audit("run_failed", &c.RunID, nil, map[string]any{"error": message})
	}
	return nil
}
