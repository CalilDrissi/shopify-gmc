// Package jobs is a small Postgres-backed job queue tailored for the audit
// pipeline. It uses SELECT FOR UPDATE SKIP LOCKED for safe concurrent claims
// across worker processes; no Redis, no other infra dependency.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	KindAuditStore = "audit_store"

	// Backoff schedule per attempt. Spec: 30s / 2min / 10min, then dead.
	MaxAttempts = 3

	defaultPollInterval = 2 * time.Second
	defaultMaxConcurrent = 3
)

// Backoffs maps the failed-attempt count (1-indexed) to the next run delay.
// After MaxAttempts the worker marks the job dead and stops re-queueing.
var Backoffs = []time.Duration{
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
}

// Handler is invoked by the worker once a job has been claimed. Returning
// nil marks the job succeeded; returning an error triggers the backoff /
// retry / dead transition.
type Handler interface {
	Handle(ctx context.Context, job Job) error
}

type HandlerFunc func(ctx context.Context, job Job) error

func (f HandlerFunc) Handle(ctx context.Context, job Job) error { return f(ctx, job) }

type Job struct {
	ID          uuid.UUID
	TenantID    *uuid.UUID
	Kind        string
	Payload     []byte
	Attempts    int
	MaxAttempts int
}

// WorkerID is the locked_by value: hostname-pid-rand6. Every worker process
// computes this once at startup and reuses it for every claim; it's how a
// future sweeper can detect crashed workers.
func WorkerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), hex.EncodeToString(suffix))
}

// Worker drains the audit_jobs table and dispatches to handlers keyed by job
// kind. One Worker is intended per process; concurrency comes from the
// MaxConcurrent semaphore.
type Worker struct {
	Pool          *pgxpool.Pool
	Logger        *slog.Logger
	WorkerID      string
	PollInterval  time.Duration
	MaxConcurrent int
	Handlers      map[string]Handler
}

func NewWorker(pool *pgxpool.Pool, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		Pool:          pool,
		Logger:        logger,
		WorkerID:      WorkerID(),
		PollInterval:  defaultPollInterval,
		MaxConcurrent: defaultMaxConcurrent,
		Handlers:      map[string]Handler{},
	}
}

func (w *Worker) Register(kind string, h Handler) { w.Handlers[kind] = h }

// Run blocks until ctx is cancelled. Polls every PollInterval; for each tick,
// claims up to (MaxConcurrent - in-flight) jobs and runs them in goroutines.
func (w *Worker) Run(ctx context.Context) error {
	w.Logger.Info("worker_start",
		slog.String("worker_id", w.WorkerID),
		slog.Int("max_concurrent", w.MaxConcurrent),
		slog.Duration("poll", w.PollInterval),
	)

	sem := make(chan struct{}, w.MaxConcurrent)
	var wg sync.WaitGroup
	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.Logger.Info("worker_stopping_drain", slog.Duration("deadline", 30*time.Second))
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select {
			case <-done:
				w.Logger.Info("worker_stopped_clean")
			case <-time.After(30 * time.Second):
				w.Logger.Warn("worker_drain_timeout",
					slog.String("hint", "in-flight jobs exceeded 30s; exiting anyway"))
			}
			return ctx.Err()
		case <-ticker.C:
			// Drain available capacity each tick.
			for {
				select {
				case sem <- struct{}{}:
				default:
					goto nextTick
				}
				job, ok, err := w.claim(ctx)
				if err != nil {
					<-sem
					w.Logger.Error("claim", slog.Any("err", err))
					goto nextTick
				}
				if !ok {
					<-sem
					goto nextTick
				}
				wg.Add(1)
				go func(j Job) {
					defer wg.Done()
					defer func() { <-sem }()
					w.handle(ctx, j)
				}(job)
			}
		nextTick:
		}
	}
}

// claim runs the SELECT FOR UPDATE SKIP LOCKED dance and returns the next job.
func (w *Worker) claim(ctx context.Context) (Job, bool, error) {
	tx, err := w.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Job{}, false, err
	}
	defer tx.Rollback(ctx)

	var (
		j   Job
		now = time.Now()
	)
	err = tx.QueryRow(ctx, `
		WITH next AS (
		  SELECT id FROM audit_jobs
		  WHERE status = 'queued' AND run_at <= $1
		  ORDER BY run_at
		  FOR UPDATE SKIP LOCKED
		  LIMIT 1
		)
		UPDATE audit_jobs aj
		SET status='running', locked_at=$1, locked_by=$2, attempts=aj.attempts+1, updated_at=now()
		FROM next
		WHERE aj.id = next.id
		RETURNING aj.id, aj.tenant_id, aj.kind, aj.payload, aj.attempts, aj.max_attempts
	`, now, w.WorkerID).Scan(&j.ID, &j.TenantID, &j.Kind, &j.Payload, &j.Attempts, &j.MaxAttempts)
	if err == pgx.ErrNoRows {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, false, err
	}
	w.Logger.Info("job_claimed",
		slog.String("job_id", j.ID.String()),
		slog.String("kind", j.Kind),
		slog.Int("attempt", j.Attempts),
	)
	return j, true, nil
}

func (w *Worker) handle(ctx context.Context, j Job) {
	h, ok := w.Handlers[j.Kind]
	if !ok {
		w.markDead(ctx, j, fmt.Errorf("no handler registered for kind=%s", j.Kind))
		return
	}
	err := h.Handle(ctx, j)
	if err == nil {
		w.markSucceeded(ctx, j)
		return
	}
	w.Logger.Warn("job_failed",
		slog.String("job_id", j.ID.String()),
		slog.Int("attempt", j.Attempts),
		slog.Any("err", err),
	)
	if j.Attempts >= j.MaxAttempts {
		w.markDead(ctx, j, err)
		return
	}
	w.markRetry(ctx, j, err)
}

func (w *Worker) markSucceeded(ctx context.Context, j Job) {
	_, err := w.Pool.Exec(ctx, `
		UPDATE audit_jobs
		SET status='succeeded', finished_at=now(), updated_at=now()
		WHERE id=$1
	`, j.ID)
	if err != nil {
		w.Logger.Error("mark_succeeded", slog.Any("err", err))
		return
	}
	w.Logger.Info("job_succeeded", slog.String("job_id", j.ID.String()))
}

func (w *Worker) markRetry(ctx context.Context, j Job, runErr error) {
	delayIdx := j.Attempts - 1
	if delayIdx < 0 {
		delayIdx = 0
	}
	if delayIdx >= len(Backoffs) {
		delayIdx = len(Backoffs) - 1
	}
	runAt := time.Now().Add(Backoffs[delayIdx])
	_, err := w.Pool.Exec(ctx, `
		UPDATE audit_jobs
		SET status='queued', last_error=$2, run_at=$3,
		    locked_at=NULL, locked_by=NULL, updated_at=now()
		WHERE id=$1
	`, j.ID, runErr.Error(), runAt)
	if err != nil {
		w.Logger.Error("mark_retry", slog.Any("err", err))
	}
}

func (w *Worker) markDead(ctx context.Context, j Job, runErr error) {
	_, err := w.Pool.Exec(ctx, `
		UPDATE audit_jobs
		SET status='dead', last_error=$2, finished_at=now(), updated_at=now()
		WHERE id=$1
	`, j.ID, runErr.Error())
	if err != nil {
		w.Logger.Error("mark_dead", slog.Any("err", err))
		return
	}
	w.Logger.Warn("job_dead", slog.String("job_id", j.ID.String()), slog.Any("err", runErr))
}
