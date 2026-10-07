package storage

import (
	"database/sql"
	"errors"
	"math"

	"github.com/khoryik96-creator/Garbage/internal/domain"
)

const retryMessage = "Page processing failed; correct the adapter and resume."

type JobStatus struct {
	State       string
	Attempts    int
	AvailableAt float64
	Error       *string
}

func (r *Repository) JobStatus(id string) (JobStatus, error) {
	var status JobStatus
	err := r.Tx.QueryRow("SELECT state,attempts,available_at,error FROM jobs WHERE id=?", id).Scan(&status.State, &status.Attempts, &status.AvailableAt, &status.Error)
	return status, found(err)
}

// Only a host holding the exclusive desktop workspace lock may reclaim leases
// immediately. Other hosts keep using expiry and token fencing in Claim.
func (r *Repository) RecoverExclusive(now float64, maxAttempts int) error {
	if _, err := r.Tx.Exec("UPDATE jobs SET lease_until=? WHERE state='leased'", now); err != nil {
		return err
	}
	return r.recoverExpired(now, maxAttempts)
}

func (r *Repository) recoverExpired(now float64, maxAttempts int) error {

	// A crashed owner consumes an attempt too. Recover a bounded batch before claiming
	// ready work; an expired token can never be renewed or commit a checkpoint.
	rows, err := r.Tx.Query("SELECT j.id,j.token,j.attempts FROM jobs j JOIN runs r ON r.id=j.id WHERE r.state IN ('queued','running') AND j.state='leased' AND j.lease_until<=? ORDER BY j.lease_until,j.id LIMIT 100", now)
	if err != nil {
		return err
	}
	var expired []domain.Claim
	for rows.Next() {
		var c domain.Claim
		if err = rows.Scan(&c.RunID, &c.Token, &c.Attempt); err != nil {
			rows.Close()
			return err
		}
		expired = append(expired, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range expired {
		if err = r.scheduleRetry(c, now, maxAttempts); err != nil {
			return err
		}
		if err = r.Audit("run_recovered", &c.RunID, nil, map[string]any{"attempt": c.Attempt, "checkpoint": "kept"}); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) Claim(now, lease float64, maxAttempts int) (*domain.Claim, error) {
	if err := r.recoverExpired(now, maxAttempts); err != nil {
		return nil, err
	}
	var c domain.Claim
	err := r.Tx.QueryRow("SELECT j.id,j.attempts FROM jobs j JOIN runs r ON r.id=j.id WHERE r.state IN ('queued','running') AND j.state='queued' AND j.available_at<=? ORDER BY j.available_at,j.id LIMIT 1", now).Scan(&c.RunID, &c.Attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.Token = domain.ID()
	c.Attempt++
	err = changed(r.Tx.Exec("UPDATE jobs SET state='leased',token=?,lease_until=?,attempts=attempts+1 WHERE id=? AND state='queued' AND available_at<=?", c.Token, now+lease, c.RunID, now))
	if err != nil {
		return nil, err
	}
	if _, err = r.Tx.Exec("UPDATE runs SET state='running',error=NULL WHERE id=?", c.RunID); err != nil {
		return nil, err
	}
	return &c, nil
}
func (r *Repository) Renew(c domain.Claim, now, lease float64) error {
	result, err := r.Tx.Exec("UPDATE jobs SET lease_until=? WHERE id=? AND token=? AND state='leased' AND lease_until>? AND EXISTS (SELECT 1 FROM runs WHERE id=? AND state IN ('queued','running'))", now+lease, c.RunID, c.Token, now, c.RunID)
	if err = changed(result, err); errors.Is(err, domain.ErrConflict) {
		return domain.ErrLease
	}
	return err
}
func (r *Repository) Release(c domain.Claim) error {
	// Shutdown is an interruption, not a provider failure. Only this token can be
	// released, so an old worker cannot reset another worker's attempts or progress.
	result, err := r.Tx.Exec("UPDATE jobs SET state='queued',token=NULL,lease_until=NULL,available_at=0,attempts=MAX(0,attempts-1) WHERE id=? AND token=? AND state='leased' AND EXISTS (SELECT 1 FROM runs WHERE id=? AND state IN ('queued','running'))", c.RunID, c.Token, c.RunID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	if _, err = r.Tx.Exec("UPDATE runs SET state='queued' WHERE id=? AND state='running'", c.RunID); err != nil {
		return err
	}
	return r.Audit("run_interrupted", &c.RunID, nil, map[string]any{"checkpoint": "kept", "next_action": "automatic retry on restart"})
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
	return r.scheduleRetry(c, now, maxAttempts)
}
func (r *Repository) scheduleRetry(c domain.Claim, now float64, maxAttempts int) error {
	state := "queued"
	if c.Attempt >= maxAttempts {
		state = "failed"
	}
	delay := math.Min(60, math.Pow(2, float64(min(c.Attempt, 6))))
	if err := changed(r.Tx.Exec("UPDATE jobs SET state=?,token=NULL,lease_until=NULL,available_at=?,error=? WHERE id=? AND token=? AND state='leased'", state, now+delay, retryMessage, c.RunID, c.Token)); err != nil {
		return err
	}
	if state == "failed" {
		if _, err := r.Tx.Exec("UPDATE runs SET state='failed',error=? WHERE id=?", retryMessage, c.RunID); err != nil {
			return err
		}
		return r.Audit("run_failed", &c.RunID, nil, map[string]any{"error": retryMessage})
	}
	_, err := r.Tx.Exec("UPDATE runs SET state='queued',error=? WHERE id=?", "Processing was interrupted or failed. The app will retry from the last saved checkpoint.", c.RunID)
	return err
}
