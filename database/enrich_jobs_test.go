package database

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEnrichJobLifecycle(t *testing.T) {
	setupUpdatesTestDB(t)
	ctx := context.Background()

	jobs := []*EnrichJob{
		{ChatID: 1, MessageID: 10, Version: "v1"},
		{ChatID: 1, MessageID: 11, Version: "v1"},
	}
	if err := EnqueueEnrichJobs(ctx, jobs); err != nil {
		t.Fatal(err)
	}
	first, err := ClaimEnrichJob(ctx)
	if err != nil || first == nil {
		t.Fatalf("claim first: %+v, %v", first, err)
	}
	second, err := ClaimEnrichJob(ctx)
	if err != nil || second == nil {
		t.Fatalf("claim second: %+v, %v", second, err)
	}
	if third, err := ClaimEnrichJob(ctx); err != nil || third != nil {
		t.Fatalf("claim empty queue: %+v, %v", third, err)
	}
	if first.ID == second.ID {
		t.Fatal("same job claimed twice")
	}

	if err := FailEnrichJob(ctx, first, errors.New("boom"), 1, time.Second); err != nil {
		t.Fatal(err)
	}
	dead, err := EnrichJobDeadPreview(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(dead) != 1 || dead[0].ID != first.ID || dead[0].LastError != "boom" {
		t.Fatalf("dead letter: %+v", dead)
	}
	counts, err := EnrichJobCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[EnrichJobDead] != 1 || counts[EnrichJobRunning] != 1 {
		t.Fatalf("counts: %+v", counts)
	}

	// Re-enqueueing a dead message revives it with a cleared attempt count.
	if err := EnqueueEnrichJobs(ctx, []*EnrichJob{{ChatID: 1, MessageID: 10, Version: "v1", Priority: 1}}); err != nil {
		t.Fatal(err)
	}
	revived, err := ClaimEnrichJob(ctx)
	if err != nil || revived == nil || revived.ID != first.ID {
		t.Fatalf("revived job: %+v, %v", revived, err)
	}
	if revived.Attempts != 0 || revived.Status != EnrichJobRunning || revived.Priority != 1 {
		t.Fatalf("revived state: %+v", revived)
	}
	if err := CompleteEnrichJob(ctx, revived.ID); err != nil {
		t.Fatal(err)
	}
	if err := PruneDoneEnrichJobs(ctx, time.Now().Unix()+1); err != nil {
		t.Fatal(err)
	}
	if got, err := ClaimEnrichJob(ctx); err != nil || got != nil {
		t.Fatalf("only non-due job should remain: %+v, %v", got, err)
	}
}

func TestEnrichJobPrepareRecoversAndResetsVersion(t *testing.T) {
	setupUpdatesTestDB(t)
	ctx := context.Background()

	if err := EnqueueEnrichJobs(ctx, []*EnrichJob{
		{ChatID: 2, MessageID: 20, Version: "v1"},
		{ChatID: 2, MessageID: 21, Version: "v1"},
	}); err != nil {
		t.Fatal(err)
	}
	running, err := ClaimEnrichJob(ctx)
	if err != nil || running == nil {
		t.Fatalf("claim: %+v, %v", running, err)
	}
	if err := PrepareEnrichJobs(ctx, "v2"); err != nil {
		t.Fatal(err)
	}
	counts, err := EnrichJobCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[EnrichJobPending] != 2 || counts[EnrichJobRunning] != 0 {
		t.Fatalf("after prepare: %+v", counts)
	}

	// A completed job with an old version is requeued for re-enrichment.
	job, err := ClaimEnrichJob(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim after prepare: %+v, %v", job, err)
	}
	if err := CompleteEnrichJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := PrepareEnrichJobs(ctx, "v3"); err != nil {
		t.Fatal(err)
	}
	requeued, err := ClaimEnrichJob(ctx)
	if err != nil || requeued == nil || requeued.ID != job.ID {
		t.Fatalf("old-version done job was not requeued: %+v, %v", requeued, err)
	}
	if requeued.Version != "v3" || requeued.Attempts != 0 {
		t.Fatalf("requeued state: %+v", requeued)
	}
}

func TestEnrichJobBackoff(t *testing.T) {
	setupUpdatesTestDB(t)
	ctx := context.Background()

	if err := EnqueueEnrichJobs(ctx, []*EnrichJob{{ChatID: 3, MessageID: 30, Version: "v1"}}); err != nil {
		t.Fatal(err)
	}
	job, err := ClaimEnrichJob(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %+v, %v", job, err)
	}
	if err := FailEnrichJob(ctx, job, errors.New("transient"), 3, time.Minute); err != nil {
		t.Fatal(err)
	}
	if next, err := ClaimEnrichJob(ctx); err != nil || next != nil {
		t.Fatalf("backoff job was claimable: %+v, %v", next, err)
	}
	if err := db.Model(&EnrichJob{}).Where("id = ?", job.ID).Update("next_run", 0).Error; err != nil {
		t.Fatal(err)
	}
	retried, err := ClaimEnrichJob(ctx)
	if err != nil || retried == nil || retried.Attempts != 1 {
		t.Fatalf("retried job: %+v, %v", retried, err)
	}
	if err := ResetEnrichJob(ctx, retried.ID); err != nil {
		t.Fatal(err)
	}
	reset, err := ClaimEnrichJob(ctx)
	if err != nil || reset == nil || reset.ID != retried.ID || reset.Attempts != 0 {
		t.Fatalf("reset job: %+v, %v", reset, err)
	}
}
