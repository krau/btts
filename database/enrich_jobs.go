package database

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	EnrichJobPending = "pending"
	EnrichJobRunning = "running"
	EnrichJobDone    = "done"
	EnrichJobDead    = "dead"
)

// EnqueueEnrichJobs inserts media jobs durably. Existing pending/running
// jobs are left untouched; done/dead jobs with an older vector version are
// reset by PrepareEnrichJobs at startup.
func EnqueueEnrichJobs(ctx context.Context, jobs []*EnrichJob) error {
	if len(jobs) == 0 {
		return nil
	}
	now := time.Now().Unix()
	for _, job := range jobs {
		job.Status = EnrichJobPending
		job.Attempts = 0
		job.NextRun = now
		job.CreatedAt = now
		job.UpdatedAt = now
	}
	return db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "chat_id"}, {Name: "message_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"status":     EnrichJobPending,
			"attempts":   0,
			"next_run":   now,
			"last_error": "",
			"mime":       gorm.Expr("excluded.mime"),
			"name":       gorm.Expr("excluded.name"),
			"priority":   gorm.Expr("excluded.priority"),
			"version":    gorm.Expr("excluded.version"),
			"updated_at": now,
		}),
		Where: clause.Where{Exprs: []clause.Expression{
			clause.Expr{SQL: "status = ?", Vars: []any{EnrichJobDead}},
		}},
	}).CreateInBatches(jobs, 200).Error
}

// ClaimEnrichJob atomically takes the next due job. nil, nil means the queue
// is currently empty.
func ClaimEnrichJob(ctx context.Context) (*EnrichJob, error) {
	now := time.Now().Unix()
	var job EnrichJob
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Where("status = ? AND next_run <= ?", EnrichJobPending, now).
			Order("priority ASC, next_run ASC, id ASC").
			First(&job).Error
		if err != nil {
			return err
		}
		res := tx.Model(&EnrichJob{}).
			Where("id = ? AND status = ?", job.ID, EnrichJobPending).
			Updates(map[string]any{"status": EnrichJobRunning, "updated_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func CompleteEnrichJob(ctx context.Context, id uint) error {
	return db.WithContext(ctx).Model(&EnrichJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     EnrichJobDone,
			"last_error": "",
			"updated_at": time.Now().Unix(),
		}).Error
}

// FailEnrichJob applies exponential backoff or moves the job to the dead
// letter state after maxAttempts.
func FailEnrichJob(ctx context.Context, job *EnrichJob, cause error, maxAttempts int, baseBackoff time.Duration) error {
	attempts := job.Attempts + 1
	status := EnrichJobPending
	nextRun := time.Now().Add(backoffDuration(baseBackoff, attempts)).Unix()
	if attempts >= maxAttempts {
		status = EnrichJobDead
		nextRun = 0
	}
	message := ""
	if cause != nil {
		message = cause.Error()
		if len(message) > 2000 {
			message = message[:2000]
		}
	}
	return db.WithContext(ctx).Model(&EnrichJob{}).
		Where("id = ?", job.ID).
		Updates(map[string]any{
			"status":     status,
			"attempts":   attempts,
			"next_run":   nextRun,
			"last_error": message,
			"updated_at": time.Now().Unix(),
		}).Error
}

func backoffDuration(base time.Duration, attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	limit := 1 << uint(min(attempts-1, 10))
	delay := base * time.Duration(limit)
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

// PrepareEnrichJobs recovers jobs interrupted by a crash and resets completed
// jobs created with an older model/dimension version.
func PrepareEnrichJobs(ctx context.Context, version string) error {
	now := time.Now().Unix()
	if err := db.WithContext(ctx).Model(&EnrichJob{}).
		Where("status = ?", EnrichJobRunning).
		Updates(map[string]any{
			"status":     EnrichJobPending,
			"next_run":   now,
			"updated_at": now,
		}).Error; err != nil {
		return err
	}
	if err := db.WithContext(ctx).Model(&EnrichJob{}).
		Where("status IN ? AND version <> ?", []string{EnrichJobDone, EnrichJobDead}, version).
		Updates(map[string]any{
			"status":     EnrichJobPending,
			"attempts":   0,
			"next_run":   now,
			"last_error": "",
			"version":    version,
			"updated_at": now,
		}).Error; err != nil {
		return err
	}
	return db.WithContext(ctx).Model(&EnrichJob{}).
		Where("status IN ? AND version <> ?", []string{EnrichJobPending}, version).
		Updates(map[string]any{"version": version, "updated_at": now}).Error
}

func EnrichJobCounts(ctx context.Context) (map[string]int64, error) {
	type row struct {
		Status string
		Total  int64
	}
	var rows []row
	err := db.WithContext(ctx).Model(&EnrichJob{}).
		Select("status, count(*) as total").
		Group("status").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.Status] = r.Total
	}
	return out, nil
}

func EnrichJobDeadPreview(ctx context.Context, limit int) ([]EnrichJob, error) {
	if limit <= 0 {
		limit = 20
	}
	var jobs []EnrichJob
	err := db.WithContext(ctx).Where("status = ?", EnrichJobDead).
		Order("updated_at DESC").Limit(limit).Find(&jobs).Error
	return jobs, err
}

func ResetEnrichJob(ctx context.Context, id uint) error {
	return db.WithContext(ctx).Model(&EnrichJob{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     EnrichJobPending,
			"attempts":   0,
			"next_run":   time.Now().Unix(),
			"last_error": "",
			"updated_at": time.Now().Unix(),
		}).Error
}

func DeleteEnrichJobsByChat(ctx context.Context, chatID int64) error {
	return db.WithContext(ctx).Where("chat_id = ?", chatID).Delete(&EnrichJob{}).Error
}

// PruneDoneEnrichJobs keeps the durable queue bounded. Completed jobs older
// than the cutoff are removed; a later re-index recreates them. Version
// upgrades only re-enrich jobs that are still retained.
func PruneDoneEnrichJobs(ctx context.Context, before int64) error {
	return db.WithContext(ctx).Where("status = ? AND updated_at < ?", EnrichJobDone, before).
		Delete(&EnrichJob{}).Error
}
