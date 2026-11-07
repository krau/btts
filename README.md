# BTTS

Better Telegram Search , 大概

Demo: [@KrauSearchBot](https://t.me/krausearchbot)

## 部署

首先部署 [meilisearch](https://www.meilisearch.com/docs/learn/self_hosted/getting_started_with_self_hosted_meilisearch) , 它是 btts 的搜索引擎.

这个安装脚本会把最新版 meilisearch 下载到当前目录:

```bash
# Install Meilisearch
curl -L https://install.meilisearch.com | sh
```

启动 meilisearch, `master-key` 为你自己设置的密钥

```bash
./meilisearch --master-key 'master-key'
```

### OCR (可选)

目前支持 [PaddleOCR](https://github.com/PaddlePaddle/PaddleOCR/) 的格式, 参考其文档的[推理部署](https://www.paddleocr.ai/latest/version3.x/deployment/serving.html), 然后使用下面的配置

```toml
[ocr]
enable = true
type = "paddle"  # 使用 PaddleOCR
[ocr.paddle]
url = "http://localhost:8000/ocr"  # PaddleOCR 服务地址
threshold = 0.8 # 置信度阈值
```

### 多模态增强（可选）

默认关闭，开启后为**非纯文本消息**提供智能检索：图片标签、图片/语音/视频音轨向量、文档（PDF）文本语义、以图搜图。纯文本消息仍然只走关键词检索，不生成 embedding，避免为每条文本消息付出不必要的索引成本。

依赖：

- [Ollama](https://ollama.com/) 与 EmbeddingGemma-2（图片/音频向量需要带 mmproj 的模型）
- 图片标签需要 [konatagger](https://github.com/krau/konatagger)
- 语音需要 `ffmpeg`；PDF 需要 `pdftotext`（poppler-utils）

```toml
# 冷历史导入，独立于 enrich：即使关闭增强也可断点续传
[import]
background = true              # 命令返回后继续在后台导入
batch_delay = "1s"             # 每 100 条历史消息的间隔

[enrich]
enable = true
workers = 2
max_download_mb = 20
max_attempts = 5
retry_backoff = "30s"
wait_index_tasks = true
media_fetch_per_second = 1     # 普通媒体下载限速；Takeout 页内也按该值串行节流
media_fetch_concurrency = 1
media_fetch_retries = 3
flood_wait_extra = "5s"

[enrich.embedder]
enable = true
# media_name = "btts_media"
url = "http://127.0.0.1:11434"          # btts -> Ollama
model = "embeddinggemma-2:latest"
dimensions = 768
timeout = "60s"
query_timeout = "10s"                    # 查询 embedding 超时，超时回退关键词检索
media_enable = true                      # 图片/音频/视频向量
media_min_score = 0.8                    # 媒体语义结果的最低相关度，按语料调整
audio_enable = false                     # 音频窗口向量，需要 ffmpeg
video_enable = true                      # 视频关键帧向量，需要支持 vision 的模型与 ffmpeg
video_frames = 3                         # 最多提取的关键帧数（每 10 秒一帧）
warmup = true
num_ctx = 8192

[enrich.tagger]
enable = false
url = "http://127.0.0.1:39771"
token = ""
threshold = 0.4
max_tags = 32

[enrich.documents]
enable = false
pdftotext = "pdftotext"
max_size_mb = 20
max_chars = 12000
```

行为说明：

- **纯文本消息不生成向量**，只参与关键词检索；不会因为开启该功能而为每条消息调用 embedding。
- 只有受支持的媒体/文档才会下载和增强；不支持的格式在下载前直接跳过，避免浪费 Telegram 流量和增强算力。当前支持的图片格式为 JPEG/PNG；音频/视频需能被 ffmpeg 解码；PDF 需要开启文档提取。
- 超过 `enrich.max_download_mb` 的媒体按预期跳过：已知大小时不下载，大小未知时下载到上限即停止；该任务标记完成，不按失败重试、不进入 dead letter。
- 只有含媒体/文档的消息才会：
  - 图片：图像向量（caption/OCR/标签作为图像 embedding 的文本上下文，不生成独立文本向量）；
  - 音频/语音：音频窗口向量；
  - 视频：关键帧图像向量（每 10 秒一帧，`video_frames` 限制数量）+ 音轨窗口向量；
  - PDF：提取文本并生成文本向量（同时写入主文档的 `extracted` 供关键词检索）。
- **caption/OCR 只进入主索引关键词字段，不做独立文本向量化**；`modality=text` 只对应 PDF/文档正文的文本向量。
- 向量写入独立的 `<index>_media_<media_name>_<dimensions>` 索引；图片/音频/视频字节不会进入 Meilisearch，只在这里存向量。
- 媒体文档带两个过滤字段：`modality`（向量类型：`text`/`image`/`audio`）和 `media_type`（来源：`image`/`audio`/`video`/`document`）。纯文本消息仍然没有向量文档。
- 文本查询会同时做：主索引关键词检索 + 媒体索引语义检索，再按 Reciprocal Rank Fusion 合并（深分页时两路各取 `offset+limit` 个候选）。纯文本消息仍可被关键词命中。
- 内联查询（inline）固定走关键词检索，避免每次按键都请求一次 embedding。
- 回复一张图片并发送 `/search`，即可用该图片检索相似消息。
- API 搜索请求支持 `tags`、`tag_match`、`mode`（`hybrid`/`keyword`/`media`）、`modality`、`image`、`audio` 字段。`modality` 指定目标：`all`（默认）、`text`、`image`、`audio`、`video`。
  - 文本查询 + `modality=image`：以文本搜图片（只匹配图片向量）；
  - 图片查询 + `modality=text`：以图搜 PDF/文档正文（caption/OCR 不生成文本向量）；
  - 图片查询 + `modality=video`：以图搜视频关键帧；
  - 音频查询 + `modality=audio|video`：以音频搜音频/视频音轨；
  - 文本查询 + `modality=video`：文本搜视频（关键帧/音轨/caption 任一向量命中）。
  - `image` 与 `audio` 互斥；音频查询不接受 `q`；`mode=media` 以文本查询时必须提供 `q`，图片/音频查询不需要；标签只存在于主索引，未开启 enrich 时 `tags` 返回 400。
  - 当前查询输入支持文本、图片、音频；视频可以作为检索目标（`modality=video`），但暂不支持把视频文件本身作为查询。
- 音频窗口按 30 秒编码，默认最多 4 个窗口；视频关键帧按 10 秒间隔提取，默认最多 3 帧。
- PDF 目前提取全文并写入 `extracted` 字段，不做页级定位。
- 更换 embedding 模型或维度时，修改 `media_name` 或 `dimensions` 会生成新的媒体索引；旧索引可手动删除。
- `enrich.embedder.enable = true` 但 Ollama/模型不可用时会启动失败并给出明确错误，避免入库任务被静默破坏。开启 `video_enable` 需要模型声明 `vision` capability。
- 媒体索引 schema 升级会改变 `enrichVersion`，保留的已完成任务会重新入队；被 prune 掉的历史任务需要 `/del` 后重新 `/add`。
- 已有消息：纯文本消息无需处理；媒体消息的向量/标签需要先 `/del` 再 `/add`（`/dl` 不会重新处理已完成任务的向量）。已经完成的聊天再次 `/add` 会被拒绝。

### 大规模冷导入 / Takeout

`/add` 把历史导入做成**可恢复、限速、后台执行**的任务，与 enrich 开关无关；大频道可选用 Telegram Takeout API：

```text
/add <chat> takeout
/addtakeout <chat>
```

Takeout 模式直接在**在线 bot 进程内**运行，不需要停 bot 或离线导出；普通实时更新继续使用普通 API。Takeout 会话会同时导出历史消息和媒体文件（`Files=true` + `FileMaxSize`），媒体下载也包在 `invokeWithTakeout` 中；图片/音频/PDF 在该页内直接增强，失败时回退到持久 `enrich_jobs` 队列。媒体下载按 `media_fetch_per_second` 限速。进度按 split range + cursor 持久化，会话失效或进程重启后再次执行命令会从断点继续。

新设备可能返回 `TAKEOUT_INIT_DELAY`，此时需要等账号满足 Takeout 条件后再执行；命令会直接报告错误，不会进行无意义的快速重试。



- 导入进度写入独立的 `index_imports` 表（`generation`、`range_index`、`cursor`、`lease_at`、`completed`），与聊天元数据、enrich 开关解耦；进程中断后再次 `/add` 或 `/addtakeout` 会从游标继续，不会从头开始。同一时间只允许一个历史导入。
- `[import] background` 控制后台执行，`[import] batch_delay` 控制每 100 条历史消息的间隔；两者与 enrich 是否开启无关。
- 媒体增强任务写入 `enrich_jobs` 表，**不会因为内存队列满而丢弃**；worker 从 SQLite 领取，失败按指数退避重试，超过 `max_attempts` 进入 dead 状态。
- 实时消息的增强任务 `priority=0`，历史导入 `priority=1`，worker 优先处理实时消息。
- 普通媒体下载经过同一组 `media_fetch_per_second` / `media_fetch_concurrency` 限速，并显式处理 Telegram `FLOOD_WAIT`；历史批量之间有抖动延迟，避免固定请求节奏。Takeout 页内媒体按 `media_fetch_per_second` 串行节流，但不与普通下载器共享并发信号量，因此两者可能短暂重叠。
- 文档写入只提交 Meilisearch 任务、不阻塞等待；索引设置等启动阶段的操作仍会等待任务完成。媒体任务在主文档暂不可见时按 500ms 间隔重试，仍未可见则按失败退避。

建议：

- 百万级频道首次导入时保持默认的保守限速，或按账号实际额度小幅提高；不要同时开多个 `/add`。
- 媒体量很大时，优先保证账号安全：`media_fetch_per_second` 建议从 1–2 开始，观察是否出现 `FLOOD_WAIT` 再调整。
- 需要查看积压时看日志中的 `Enrichment queue: pending=... dead=...`，或使用 `/enrich` 查看计数和最近的 dead 任务；`/enrich retry <id>` 可重新入队。

### 测试

单元测试不依赖外部服务：

```bash
go test ./...
go test -race ./...
```

集成测试使用真实的 Meilisearch 与 Ollama，默认不参与 `go test ./...`：

```bash
docker run -d --rm --name btts-meili-test -p 127.0.0.1:7700:7700 getmeili/meilisearch:latest

BTTS_TEST_MEILI_URL=http://127.0.0.1:7700 \
BTTS_TEST_OLLAMA_URL=http://127.0.0.1:11434 \
BTTS_TEST_OLLAMA_MODEL=embeddinggemma-2:latest \
BTTS_TEST_PDF_DIR=/tmp/pdfs \
go test -tags=integration ./engine/meili ./service/enrich -v
```

覆盖范围：Meilisearch 索引 schema、部分更新保留 `tags`/`extracted`、删除后不重建、标签过滤、媒体向量与混合检索/RRF 分页/以图搜图；Ollama 真实文本/图片/音频 embedding（模型需声明 `vision`/`audio`，否则自动跳过对应断言）；跨模态目标检索（文本↔图片↔音频，以及视频关键帧/音轨）；真实 PDF 的文本提取与语义检索；视频关键帧提取、音频窗口切分（ffmpeg）；tagger 响应解析。历史导入（普通分页、Takeout 分页、断点、失败回退）使用假 Telegram invoker + 真实 SQLite。

`BTTS_TEST_OLLAMA_DIMENSIONS` 可覆盖维度断言（默认 768）；`BTTS_TEST_PDF_DIR` 默认 `/tmp/pdfs`。PDF 目录不存在或不足两个可提取文本的 PDF 时，PDF 语义测试自动跳过。

### 配置和启动

在本项目 [release](https://github.com/krau/btts/releases) 页面下载最新 btts 版本并解压, 然后进入解压后的目录.

创建 `config.toml` 配置文件, 参考下面的配置:

```toml
# Telegram Bot 配置
app_id = 123
app_hash = "1234567890abcdef1234567890abcdef"
bot_token= "1234567890:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
admins = [1234567890, 1234567890]
[engine]
type = "meilisearch"  # 使用 meilisearch 引擎
url = "http://localhost:7700"
key = "master-key"
index = "btts" # 索引名称
[api]
# 可选, 开启 api 和 web 界面
enable = true
addr = "127.0.0.1:39415"
key = "qwqowo" # api 密钥, 访问时需要提供
```

启动 !

## 使用

第一次启动时, 会要求输入手机号登录账号.

启动时会恢复离线期间的更新；恢复初始化失败会停止程序并报告错误，不会继续显示启动成功。请排除错误后重新启动。

/add - 添加一个聊天进行索引, 会自动监听聊天的新消息

/del - 删除聊天及其索引数据，并取消索引监听；不会退出该聊天或禁用用户账号插件

可自定义是否监听以及是否监听消息删除事件

/unwatch - 停止索引该聊天的新消息，但保留已有索引数据；用户账号插件不受影响

/watch - 监听一个聊天

/unwatchdel - 不监听一个聊天的消息删除事件

/watchdel - 监听一个聊天的消息删除事件

取消索引监听不等于断开用户账号与 Telegram 的更新处理。恢复聊天更新时仍会遵守索引监听设置，不会自动重建已删除的索引。

升级会保留原有的离线恢复进度。命令行 `btts takeout` 使用导出专用客户端，不消费实时更新；bot 内的 `/addtakeout` 则在在线进程中运行，实时更新继续处理。

可创建子 bot , 子 bot 只有搜索功能且只能搜索指定的一些聊天, 这在为某些频道提供专属搜索功能时非常有用

/addsub - 添加一个子 bot

/delsub - 删除一个子 bot

/lssub - 列出所有子 bot

可创建子 api key, 同子 bot 的设计一样, 子 api key 只能搜索指定的一些聊天, 在 web 端填入后即可使用

/addapikey - 添加一个子 api key

/delapikey - 删除一个子 api key

/lsapikey - 列出所有子 api key

### 用户账号插件

启用 `[plugin] enable = true` 后，在登录账号自己发送的消息中使用命令；默认前缀为 `,`，可通过 `prefixes` 配置。

- 回复消息后发送 `,re [次数]`：默认转发 1 次，可指定 1–100 次；删除命令后逐次转发，个别请求失败仍继续剩余次数，错误记入日志。账号停止时取消剩余操作。
- 在有管理员权限的群内发送 `,clean_member`，按提示选择查找或清理。成员分页未返回新成员时会中止并提示处理失败，避免重复处理和持续请求。



---

## 从 V0 迁移到 V1

> V1 没有支持 Bleve 引擎, 如有需要请勿升级.

在 Release 页面下载 V1 版本的二进制文件, 解压后替换掉旧版本的二进制文件, 同时配置好 `config.toml` 文件.

使用以下命令启动迁移:

```bash
./btts migrate
```

或者, 可以在运行期间在后台迁移:

```bash
./btts --migrate
```