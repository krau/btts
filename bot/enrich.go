package bot

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/krau/btts/database"
	"github.com/krau/mygotg/dispatcher"
	"github.com/krau/mygotg/ext"
)

func EnrichStatusHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	if !bi.Engine.Capabilities().Enrichment {
		ctx.Reply(update, ext.ReplyTextString("Media enrichment is not enabled"), nil)
		return dispatcher.EndGroups
	}
	args := update.Args()
	if len(args) >= 3 && args[1] == "retry" {
		id, err := strconv.ParseUint(args[2], 10, 64)
		if err != nil {
			ctx.Reply(update, ext.ReplyTextString("Usage: /enrich retry <job_id>"), nil)
			return dispatcher.EndGroups
		}
		if err := database.ResetEnrichJob(ctx, uint(id)); err != nil {
			ctx.Reply(update, ext.ReplyTextString("Failed to retry job: "+err.Error()), nil)
			return dispatcher.EndGroups
		}
		ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("Job %d queued again", id)), nil)
		return dispatcher.EndGroups
	}
	counts, err := database.EnrichJobCounts(ctx)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to read enrichment queue: "+err.Error()), nil)
		return dispatcher.EndGroups
	}
	var sb strings.Builder
	sb.WriteString("Enrichment queue:\n")
	sb.WriteString(fmt.Sprintf("pending: %d\nrunning: %d\ndone: %d\ndead: %d\n",
		counts[database.EnrichJobPending], counts[database.EnrichJobRunning],
		counts[database.EnrichJobDone], counts[database.EnrichJobDead]))
	dead, err := database.EnrichJobDeadPreview(ctx, 5)
	if err == nil && len(dead) > 0 {
		sb.WriteString("\nRecent dead jobs:\n")
		for _, job := range dead {
			sb.WriteString(fmt.Sprintf("#%d %d_%d %s\n", job.ID, job.ChatID, job.MessageID, job.LastError))
		}
		sb.WriteString("\nUse /enrich retry <id> to retry one.")
	}
	ctx.Reply(update, ext.ReplyTextString(sb.String()), nil)
	return dispatcher.EndGroups
}
