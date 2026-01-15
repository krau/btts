package cmd

import (
	"github.com/charmbracelet/log"
	"github.com/krau/btts/config"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/userclient"
	"github.com/spf13/cobra"
)

func RegisterTakeoutCmd(root *cobra.Command) {
	var enableWatching bool
	takeoutCmd := &cobra.Command{
		Use:   "takeout",
		Short: "Export all chat messages to index using Telegram Takeout API",
		Long: `Export all chat messages from your Telegram account to the search index using Takeout API.
This will:
1. Initialize a Takeout session with Telegram
2. Fetch all dialogs (chats, groups, channels)
3. Export message history from each dialog
4. Index all messages into the search engine

Note: This is a one-time export operation and may take a long time depending on the amount of data.`,
		Run: func(cmd *cobra.Command, args []string) {
			ctx := cmd.Context()
			logger := log.FromContext(ctx)

			// 初始化配置
			config.Init()

			// 初始化数据库
			if err := database.InitDatabase(ctx); err != nil {
				logger.Fatal("Failed to initialize database", "error", err)
				return
			}

			// 初始化 UserClient
			uc, err := userclient.NewUserClient(ctx)
			if err != nil {
				logger.Fatal("Failed to initialize user client", "error", err)
				return
			}

			// 初始化搜索引擎
			if _, err := engine.NewEngine(ctx); err != nil {
				logger.Fatal("Failed to initialize search engine", "error", err)
				return
			}

			// 使用 bubbletea 进度条运行 takeout 导出
			if err := runTakeoutWithProgress(func(progressCallback func(stage string, current, total int, message string)) error {
				return uc.TakeoutExport(ctx, enableWatching, progressCallback)
			}); err != nil {
				logger.Fatal("Takeout export failed", "error", err)
			}
		},
	}

	// 是否将导出的聊天设置为默认监听
	takeoutCmd.Flags().BoolVar(&enableWatching, "watch", false, "Mark exported chats as watching (default: false)")

	root.AddCommand(takeoutCmd)
}
