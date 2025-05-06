# BTTS

Better Telegram Search , 大概

Demo: [@KrauSearchBot](https://t.me/krausearchbot)

## 使用

在 release 页面下载最新预购建, 并自行部署 [MeiliSearch](https://www.meilisearch.com/docs/home) 

然后新建 `config.toml`:

```toml
app_id = 123
app_hash = "1234567890abcdef1234567890abcdef"
bot_token= "1234567890:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
admins = [1234567890, 1234567890]
[engine]
url = "http://localhost:7700"
key = "master-key"
```

启动 !