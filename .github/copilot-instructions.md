# BTTS 项目 AI 开发说明

本仓库是一个基于 Telegram 的搜索机器人与 Web/API 服务，用于搜索 Telegram 上的聊天消息, 核心组件包括：

- `bot/`：Bot 客户端（`gotgproto`），负责处理 `/add`、`/del`、`/watch` 等命令和搜索交互。
- `userclient/`：用户账号客户端，用于加入/监听群组、接收消息更新并驱动索引更新。
- `engine/`：搜索引擎封装，当前实现为 Meilisearch，每个聊天一个索引（`btts_<chatID>`）。
- `database/`：基于 SQLite + gorm 的持久层，存储索引聊天、用户信息、子 bot 等。
- `api/`：基于 Fiber 的 HTTP API 与 Web UI，提供搜索接口与文件流访问。
- `config/`：使用 `viper` 加载 `config.toml` 和环境变量到全局配置 `config.C`。

## 启动与运行方式

- 入口：`main.go` 调用 `cmd.Execute()`，命令行子命令在 `cmd/` 目录下。
- 配置：启动前需在仓库根目录准备 `config.toml`，字段结构见 `config/config.go` 与 `README.md` 示例。
- 搜索引擎：`engine.NewEngine` 使用 `config.C.Engine` 连接 Meilisearch，并按聊天 ID 创建/更新索引。
- 数据库：`database.InitDatabase` 在 `data/` 下创建 `data.db`，并自动迁移 `UserInfo`、`IndexChat`、`SubBot` 等模型。
- Telegram 客户端：
  - `bot.NewBot` 使用 `config.C.BotToken` 创建 Bot 客户端，session 存在 `data/session_bot.db`。
  - `userclient.NewUserClient` 使用手机号登录的 User 客户端，session 存在 `data/session_user.db`，日志写入 `data/logs/client.jsonl`。

在添加新功能时，请沿用以上启动流程与目录约定，避免引入与现有 CLI/配置不兼容的入口。

## 典型数据流与调用链

- **消息索引流程**：
  - `userclient.StartWatch` 注册对消息与删除事件的 handler（`WatchHandler`、`DeleteHandler`）。
  - 仅当 `database.Watching(chatID)` 为真时，消息才会进入索引逻辑。
  - 相关消息转换/下载工具位于 `utils/`，索引写入通过 `engine.Engine` 完成。
- **搜索流程（Bot 内）**：
  - Bot 命令 handler 在 `bot/` 子文件中（例如 `search.go`、`list.go`）。
  - handler 解析命令参数、确定目标聊天 ID 列表，构造 `types.SearchRequest`。
  - 调用 `engine.Engine.Search` 或 `multiSearch`，得到 `types.MessageSearchResponse` 并格式化为 Telegram 消息回复。
- **搜索流程（HTTP API）**：
  - `api/api.go` 中注册 `/api/index/:chat_id/search`、`/api/index/multi-search` 等路由。
  - 请求体验证使用 `validator.v10` 和项目内的 `types` 结构体，JSON 编码使用 `sonic`。
  - 统一通过 `engine.Engine` 调用 Meilisearch；文件流由 `service.GetTGFileReader` 和 `utils.NewDownloader` 组合实现。

## 配置与安全约定

- 全局配置集中在 `config.C`，不要在业务代码中自行解析配置文件或环境变量。
- HTTP API 授权：
  - 主 API 通过 `keyauth` 校验 `Authorization` header，密钥来源 `config.C.Api.Key`。
  - `/api/client/filestream` 使用基于 `chat_id`、`message_id` 和 `config.C.Api.Key` 派生的 `reqtoken` 查询参数，详见 `api.Serve` 中嵌套验证逻辑。
- 日志：
  - 使用 `github.com/charmbracelet/log` 的 `log.FromContext(ctx)` 记录上下文日志。
  - 生成新的 handler 或服务函数时，优先从上下文获取 logger，而不是创建全局 logger。

## 代码结构与模式

- **单例/全局实例**：
  - `bot.GetBot`、`engine.GetEngine`、`userclient.GetUserClient` 等均依赖先调用对应 `NewXxx` 完成初始化后再访问。
  - 在新增逻辑时，避免在包初始化阶段隐式调用这些单例构造函数，而是遵循现有启动顺序（通常在 `cmd/` 或服务启动流程中显式初始化）。
- **上下文传播**：
  - 绝大多数公共函数（例如 `service.GetTGFileReader`、`database` 包接口）都以 `context.Context` 作为第一个参数，用于日志与取消信号。
  - 在新增函数时，请保持这一风格，并传递已有的 `ctx`，不要使用 `context.Background()` 替代。
- **类型与 DTO**：
  - 与 Meilisearch 和 API 交互的结构集中在 `types/` 与 `api/types.go`（如 `types.SearchRequest`、`types.SearchHit`）。
  - 扩展搜索参数或响应字段时，请优先修改这些类型，然后在 `engine` 与 `api` 中按需串联，而不是直接使用 `map[string]any`。

## 对 AI 代理的具体建议

- 在修改 search/索引逻辑前，优先查阅：`engine/engine.go`、`types/`、`database/model.go`、`bot/search.go` 与相关 API handler。
- 为新的命令或 Bot 行为添加逻辑时，应在 `bot/` 目录中新建文件或复用现有 handler 模式，而不是将逻辑塞入 `bot.go`。
- 为 HTTP 功能扩展时，请在 `api/api.go` 中注册路由，并将业务逻辑拆分到独立函数/文件（遵循现有 `GetIndexed`、`SearchOnChatByPost` 等风格）。
- 如需持久化新数据，先在 `database/model.go` 增加模型并更新相应访问方法，再通过 `InitDatabase` 自动迁移；避免在其他包中直接操作 `gorm.DB`。
