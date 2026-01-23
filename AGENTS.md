# BTTS 项目 AI 开发说明

本仓库是一个基于 Telegram 的搜索机器人与 Web/API 服务，用于搜索 Telegram 上的聊天消息。

## 架构概览

核心组件与职责边界：

- `bot/`：Bot 客户端（基于 `mygotg`），处理管理员命令（`/add`、`/del`、`/watch`）和用户搜索交互
- `userclient/`：User 客户端，监听群组消息更新并驱动实时索引，session 存储在 `data/session_user.db`
- `engine/`：搜索引擎抽象层（当前为 Meilisearch），每个聊天对应独立索引 `btts_<chatID>`
- `database/`：SQLite + gorm 持久层，存储 `IndexChat`、`UserInfo`、`SubBot`、`ApiKey` 模型
- `api/`：Fiber HTTP 服务，提供搜索 API、Web UI 和文件流代理（`/api/client/filestream`）
- `subbot/`：子 bot 管理器，每个子 bot 独立运行、拥有独立 session、仅能搜索指定聊天列表
- `config/`：使用 `viper` 从 `config.toml` 和环境变量（前缀 `BTTS_`）加载配置到 `config.C`

**为什么有两个 Telegram 客户端**：Bot 客户端权限受限无法加入私有群组，User 客户端以用户身份运行可监听所有已加入的聊天。

## 启动流程与初始化顺序

入口：`main.go` → `cmd.Execute()` → `cmd/run.go` 中的初始化顺序**必须**严格遵守：

```go
1. config.Init()              // 加载配置
2. database.InitDatabase()    // 初始化 DB，自动迁移模型，加载 watchedChatsID 缓存
3. userclient.NewUserClient() // 首次启动需交互式登录手机号
4. engine.NewEngine()         // 连接 Meilisearch 并健康检查
5. bot.NewBot()               // 初始化主 bot
6. api.Serve()                // 可选，启动 HTTP 服务（config.C.Api.Enable）
7. bot.Start()                // 注册 handlers 并阻塞运行
```

**关键点**：所有 `Get*()` 单例访问器（`bot.GetBot()`、`engine.GetEngine()` 等）依赖先调用 `New*()` 初始化，否则会 panic。

## 消息索引数据流（实时索引核心）

```
Telegram 消息更新
  ↓
userclient.WatchHandler (dispatcher handler)
  ↓
database.Watching(chatID) 检查是否监听
  ↓ (true)
database.UpsertIndexChat/UpsertUserInfo 更新元数据
  ↓
engine.DocumentsFromMessages 转换为 types.MessageDocument
  ├── utils.GetCaption 提取文本
  ├── utils.FileFromMedia 提取媒体元数据
  └── OCR 处理（可选，config.C.Ocr.Enable）
  ↓
engine.AddDocuments → Meilisearch 索引写入
```

**触发条件**：只有 `database.Watching(chatID) == true` 的聊天才会索引新消息（通过 `/watch` 命令控制，默认 `/add` 后自动监听）。

**删除事件**：`userclient.DeleteHandler` 监听 `UpdateDeleteChannelMessages` 和 `UpdateDeleteMessages`，调用 `engine.DeleteDocuments` 从索引中移除（需 `IndexChat.NoDelete == false`）。

## 搜索流程两条路径

### Bot 内搜索（Telegram 交互）

```
用户消息/命令
  ↓
bot/search.go: SearchHandler
  ↓
解析查询 + 确定目标聊天列表（当前频道/私聊全聊天/inline 查询）
  ↓
engine.Search(types.SearchRequest)
  ↓
utils.BuildSearchReplyMarkup 构造 inline keyboard 分页按钮
  ↓
ctx.Reply 回复结果（最多 5 条）+ 回调处理翻页/过滤器
```

**权限控制**：`CheckPermission()` 检查是否为 admin（`config.C.Admins`），普通用户只能搜索公开聊天或自己参与的聊天。

### HTTP API 搜索

```
POST /api/index/:chat_id/search
  ↓
api.SearchOnChatByPost (keyauth 中间件鉴权)
  ↓
validateApiKey 检查主 API key 或子 API key
  ├── 主 key: ctx.Locals("api_master") = true
  └── 子 key: ctx.Locals("api_key_chats") = 可访问聊天列表
  ↓
engine.Search(types.SearchRequest)
  ↓
JSON 响应（sonic 编码）
```

**API 鉴权逻辑**（`api/api.go`）：
- 主 API key：`Authorization` header 匹配 `sha256(config.C.Api.Key)`，全权限
- 子 API key：查询 `ApiKey` 表（`KeyHash` 字段），限制访问 `ApiKey.Chats` 列表

**文件流代理**：`/api/client/filestream` 使用自定义 `reqtoken` 查询参数（基于 `chat_id` + `message_id` + `config.C.Api.Key` 计算 SHA-256），避免 Bearer token 泄露。

## mygotg Dispatcher 模式（Bot Handler 开发）

使用 `dispatcher.HandlerFunc` 返回值控制 handler 链流转：

```go
dispatcher.EndGroups        // 终止所有后续 handler 执行（命令处理完成）
dispatcher.SkipCurrentGroup // 跳过当前 group，继续下一个 group（过滤不符合条件的更新）
dispatcher.ContinueGroups   // 继续执行当前 group 的下一个 handler
```

**示例**（`userclient/watch.go`）：
```go
if u.EffectiveMessage == nil {
    return dispatcher.SkipCurrentGroup // 不是消息更新，跳过索引逻辑
}
// ... 索引处理
return dispatcher.SkipCurrentGroup // 完成后跳过避免重复处理
```

**注册 Handler 模式**（`bot/handler.go`）：
```go
disp.AddHandler(handlers.NewCommand("search", SearchHandler))
disp.AddHandler(handlers.NewCallbackQuery(filters.CallbackQuery.Prefix("search"), SearchCallbackHandler))
disp.AddHandler(handlers.NewMessage(filters.Message.ChatType(filters.ChatTypeUser), SearchHandler))
```

**新增命令步骤**：
1. 在 `bot/` 下创建新文件（如 `mycommand.go`）
2. 实现 `func MyCommandHandler(ctx *ext.Context, update *ext.Update) error`
3. 在 `bot/handler.go` 的 `commandHandlers` 切片中注册
4. 返回 `dispatcher.EndGroups` 结束处理

## 日志与上下文约定

**严格要求**：
- 所有公共函数第一个参数必须是 `context.Context`（用于日志传播和取消信号）
- 使用 `log.FromContext(ctx)` 获取 logger，**禁止**创建全局 logger
- 在 `cmd/run.go` 中通过 `log.WithContext(ctx, logger)` 注入 logger

**日志格式**：
```go
logger.Info("Message indexed", "chat_id", chatID, "message_id", msgID)
logger.Errorf("Failed to fetch: %v", err) // 仅用于致命错误
```

**上下文传播示例**（`service/file.go`）：
```go
func GetTGFileReader(ctx context.Context, chatID int64, messageId int) (*TGFileFileReader, error) {
    logger := log.FromContext(ctx)
    // ... 业务逻辑
}
```

## 类型系统与数据模型

### 核心类型位置

- `types/message.go`：`MessageDocument`（索引文档）、`SearchRequest`、`SearchResponse`、`MessageType` 枚举
- `database/model.go`：`IndexChat`、`UserInfo`、`SubBot`、`ApiKey`（gorm 模型）
- `api/types.go`：HTTP 请求/响应 DTO

### 扩展搜索参数示例

修改 `types.SearchRequest` 添加字段 → 在 `engine/meili/meilisearch.go` 中映射到 Meilisearch 查询参数 → API handler 自动通过 validator 验证。

### 数据库模型约定

- 主键使用 `ChatID int64` 或 `ID uint`
- 多对多关系通过 `gorm:"many2many"` 定义（见 `IndexChat.Members`、`ApiKey.Chats`）
- JSON 序列化字段使用 `gorm:"serializer:json;type:json"`（见 `SubBot.ChatIDs`）
- **禁止**在业务代码直接访问 `gorm.DB`，必须通过 `database/dao.go` 封装的方法

## 开发工作流

### 本地运行

```bash
# 首次需配置 config.toml（参考 README.md）
go run main.go  # 自动执行 cmd/run.go
# 首次启动需交互式登录 Telegram 账号（扫码/验证码）
```

### 生成 Swagger 文档

```bash
go generate  # 执行 main.go 中的 //go:generate swag init
# 访问 http://localhost:39415/swagger/index.html
```

### 测试

```bash
go test ./types -v  # 仅有 types_test.go 包含单元测试
```

**测试覆盖极少**：当前仅测试 `SearchRequest.FilterExpression()` 方法，需补充集成测试。

## 常见陷阱与调试建议

1. **单例未初始化 panic**：确保 `New*()` 在 `Get*()` 之前调用，遵循 `cmd/run.go` 顺序
2. **消息未索引**：检查 `database.Watching(chatID)` 状态，使用 `/watch <chatID>` 启用
3. **API 401 Unauthorized**：验证 `Authorization: Bearer <key>` header，或检查子 API key 权限范围
4. **文件下载失败**：尝试先用 bot client 再用 user client（见 `service.GetTGFileReader` fallback 逻辑）
5. **日志丢失上下文**：检查是否正确传递 `ctx` 而非 `context.Background()`
6. **Dispatcher handler 执行多次**：确保在逻辑结束时返回 `dispatcher.EndGroups` 或 `SkipCurrentGroup`

## 新增功能检查清单

- [ ] 需要持久化？在 `database/model.go` 添加模型 → 更新 `InitDatabase` AutoMigrate → 在 `dao.go` 添加 CRUD 方法
- [ ] 新 Bot 命令？在 `bot/` 创建文件 → 在 `handler.go` 注册 → 返回正确 dispatcher 控制流
- [ ] 新 HTTP 路由？在 `api/api.go` 注册 → 使用 `validator` 验证请求体 → 通过 `sonic` 编码响应
- [ ] 修改搜索逻辑？更新 `types.SearchRequest` → 修改 `engine/meili/meilisearch.go` 映射
- [ ] 新配置项？在 `config/config.go` 的 `AppConfig` 添加字段 → 在 `config.toml` 中配置
- [ ] 需要 Meilisearch 索引字段？修改 `types.MessageDocument` → 在 `engine.DocumentsFromMessages` 填充
