package migrate

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/charmbracelet/log"
	"github.com/krau/btts/config"
	"github.com/krau/btts/database"
	"github.com/krau/btts/types"
	"github.com/krau/btts/utils"
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
			ctx := cmd.Context()
			logger := log.FromContext(ctx)
			logger.Info("Starting migration...")
			if err := migrateToV1(ctx, dropOld); err != nil {
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

func migrateToV1(ctx context.Context, dropOld bool) error {
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
		Uid:        "btts",
		PrimaryKey: "id",
	})
	if err != nil {
		return fmt.Errorf("failed to create new index: %w", err)
	}
	logger.Info("Created new index 'btts'")
	newIndex := client.Index("btts")
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
		if err := migrateChat(ctx, oldIndex, newIndex); err != nil {
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

func migrateChat(ctx context.Context, oldIndex, newIndex meilisearch.IndexManager) error {
	logger := log.FromContext(ctx)
	stats, err := oldIndex.GetStatsWithContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get old index stats: %w", err)
	}
	total := stats.NumberOfDocuments
	if total == 0 {
		return nil
	}
	logger.Info("Migrating documents", "total", total)
	offset, batchSize := int64(0), int64(1000)
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
		hitBytes, err := sonic.Marshal(resp.Results)
		if err != nil {
			return fmt.Errorf("failed to marshal documents: %w", err)
		}
		hits := make([]*types.MessageDocumentV1, 0, len(resp.Results))
		err = sonic.Unmarshal(hitBytes, &hits)
		if err != nil {
			return fmt.Errorf("failed to unmarshal documents: %w", err)
		}
		// 新的 ID: chat_id 和 message_id 进行 Cantor 配对
		// 把原先的ID设到 message_id 字段
		for _, hit := range hits {
			newID := utils.CantorPair(uint64(hit.ChatID), uint64(hit.ID))
			hit.MessageID = hit.ID
			hit.ID = int64(newID)
		}
		priKey := "id"
		_, err = newIndex.UpdateDocumentsWithContext(ctx, hits, &priKey)
		if err != nil {
			return fmt.Errorf("failed to update documents: %w", err)
		}
		offset += batchSize
	}
	return nil
}
