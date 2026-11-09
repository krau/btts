package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/charmbracelet/log"
	"github.com/krau/btts/config"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine/meili"
	"github.com/meilisearch/meilisearch-go"
	"github.com/spf13/cobra"
)

/*
v1 主要变更是更改了索引切分策略
对于 meilisearch 引擎, 把消息存到单一索引中, 通过 chat_id 字段进行区分
*/

func RegisterCmd(root *cobra.Command) {
	var dropOld bool
	migrateCmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate database to v1 format",
		Run: func(cmd *cobra.Command, args []string) {
			config.Init()
			ctx := cmd.Context()
			logger := log.FromContext(ctx)
			logger.Info("Starting migration...")
			if err := MigrateToV1(ctx, dropOld); err != nil {
				logger.Error("Migration failed", "error", err)
				return
			}
		},
	}
	migrateCmd.Flags().BoolVar(&dropOld, "drop-old", false, "Drop old indexes after migration")
	root.AddCommand(migrateCmd)
}

func indexKey(chatID int64) string {
	return fmt.Sprintf("btts_%d", chatID)
}

func MigrateToV1(ctx context.Context, dropOld bool) error {
	logger := log.FromContext(ctx)
	logger.Info("Starting migration to v1 format")
	cfg := config.C
	if cfg.Engine.Type != "meilisearch" {
		return fmt.Errorf("migration to v1 is only supported for meilisearch engine")
	}
	client := meilisearch.New(cfg.Engine.Url, meilisearch.WithAPIKey(cfg.Engine.Key))
	_, err := client.HealthWithContext(ctx)
	if err != nil {
		return fmt.Errorf("meilisearch health check failed: %w", err)
	}
	_, err = client.CreateIndexWithContext(ctx, &meilisearch.IndexConfig{
		Uid:        cfg.Engine.Index,
		PrimaryKey: "id",
	})
	if err != nil {
		return fmt.Errorf("failed to create new index: %w", err)
	}
	logger.Info("Created new index", "index", cfg.Engine.Index)
	newIndex := client.Index(cfg.Engine.Index)
	_, err = newIndex.UpdateSettingsWithContext(ctx, &meilisearch.Settings{
		FilterableAttributes: []string{
			"user_id",
			"chat_id",
			"type",
			"timestamp",
		},
		SortableAttributes: []string{
			"timestamp",
			"id",
			"chat_id",
			"message_id",
		},
		SearchableAttributes: []string{
			"message", "ocred", "aigenerated",
		},
	})
	if err != nil {
		return fmt.Errorf("failed to update new index settings: %w", err)
	}
	logger.Info("Updated new index settings")
	if err := database.InitDatabase(ctx); err != nil {
		return err
	}
	chats := database.AllChatIDs()
	for _, chatID := range chats {
		logger.Info("Migrating chat", "chat_id", chatID)
		oldIndex := client.Index(indexKey(chatID))
		if err := migrateChat(ctx, chatID, oldIndex, newIndex); err != nil {
			return fmt.Errorf("failed to migrate chat %d: %w", chatID, err)
		}
		if dropOld {
			logger.Info("Dropping old index", "index", indexKey(chatID))
			_, err := client.DeleteIndexWithContext(ctx, indexKey(chatID))
			if err != nil {
				logger.Warn("Failed to drop old index", "index", indexKey(chatID), "error", err)
			} else {
				logger.Info("Dropped old index", "index", indexKey(chatID))
			}
		}
	}
	logger.Info("Migration completed successfully")
	return nil
}

func migrateChat(ctx context.Context, chatID int64, oldIndex, newIndex meilisearch.IndexManager) error {
	logger := log.FromContext(ctx)
	stats, err := oldIndex.GetStatsWithContext(ctx, &meilisearch.StatsParams{})
	if err != nil {
		return fmt.Errorf("failed to get old index stats: %w", err)
	}
	total := stats.NumberOfDocuments
	if total == 0 {
		return nil
	}
	// The destination may contain documents from an earlier migration. Replace
	// the chat instead of merging, otherwise stale primary keys survive.
	if _, err := newIndex.DeleteDocumentsByFilterWithContext(
		ctx, fmt.Sprintf("chat_id = %d", chatID), nil,
	); err != nil {
		return fmt.Errorf("clear existing documents for chat %d: %w", chatID, err)
	}
	logger.Info("Migrating documents", "total", total, "chat_id", chatID)
	offset, batchSize := int64(0), int64(1000)
	skipped := 0
	for {
		var resp meilisearch.DocumentsResult
		err := oldIndex.GetDocumentsWithContext(ctx, &meilisearch.DocumentsQuery{
			Offset: offset,
			Limit:  batchSize,
		}, &resp)

		if err != nil {
			return fmt.Errorf("failed to get documents: %w", err)
		}
		if len(resp.Results) == 0 {
			if offset >= total {
				break
			}
			return fmt.Errorf("no documents returned but offset %d < total %d", offset, total)
		}
		hitBytes, err := json.Marshal(resp.Results)
		if err != nil {
			return fmt.Errorf("failed to marshal documents: %w", err)
		}
		hits := make([]*legacyDocument, 0, len(resp.Results))
		err = json.Unmarshal(hitBytes, &hits)
		if err != nil {
			return fmt.Errorf("failed to unmarshal documents: %w", err)
		}
		newDocs := make([]*meili.MeilisearchMessageDocument, 0, len(hits))
		for _, hit := range hits {
			messageID := hit.telegramMessageID()
			// The legacy index's primary key was not always the Telegram
			// message ID (Cantor pairing or an auto-assigned id). Only the
			// message_id field is trustworthy when it is present.
			if messageID <= 0 || messageID > math.MaxInt32 {
				skipped++
				logger.Warn("Skipping legacy document without a valid message id",
					"chat_id", chatID, "legacy_id", hit.ID, "message_id", hit.MessageID)
				continue
			}
			newDocs = append(newDocs, &meili.MeilisearchMessageDocument{
				ID:          fmt.Sprintf("%d_%d", chatID, messageID),
				Type:        hit.Type,
				Message:     hit.Message,
				Ocred:       hit.Ocred,
				AIGenerated: hit.AIGenerated,
				Tags:        hit.Tags,
				Extracted:   hit.Extracted,
				UserID:      hit.UserID,
				ChatID:      chatID,
				MessageID:   messageID,
				Timestamp:   hit.Timestamp,
			})
		}
		if len(newDocs) > 0 {
			_, err = newIndex.AddDocumentsWithContext(ctx, newDocs, &meilisearch.DocumentOptions{
				PrimaryKey: new("id"),
			})
			if err != nil {
				return fmt.Errorf("failed to update documents: %w", err)
			}
		}
		offset += batchSize
	}
	if skipped > 0 {
		logger.Warn("Skipped legacy documents", "chat_id", chatID, "count", skipped)
	}
	return nil
}

type legacyDocument struct {
	// ID was the primary key of the old per-chat index. It was a message id
	// before the Cantor-pairing scheme, and a Cantor pair or auto id after it.
	ID          int64    `json:"id"`
	Type        int      `json:"type"`
	Message     string   `json:"message"`
	Ocred       string   `json:"ocred"`
	AIGenerated string   `json:"aigenerated"`
	Tags        []string `json:"tags,omitempty"`
	Extracted   string   `json:"extracted,omitempty"`
	UserID      int64    `json:"user_id"`
	ChatID      int64    `json:"chat_id"`
	// MessageID is the Telegram message id used by the Cantor-era documents.
	MessageID int64 `json:"message_id"`
	Timestamp int64 `json:"timestamp"`
}

func (d legacyDocument) telegramMessageID() int64 {
	if d.MessageID > 0 {
		return d.MessageID
	}
	return d.ID
}
