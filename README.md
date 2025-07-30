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

然后在本项目 [release](https://github.com/krau/btts/releases) 页面下载最新 btts 版本并解压, 然后进入解压后的目录.

创建 `config.toml` 配置文件, 参考下面的配置:

```toml
# Telegram Bot 配置
app_id = 123
app_hash = "1234567890abcdef1234567890abcdef"
bot_token= "1234567890:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
admins = [1234567890, 1234567890]
[engine]
# meilisearch 配置
url = "http://localhost:7700"
key = "master-key"
[api]
# 可选, 开启 api 和 web 界面
enable = true
addr = "127.0.0.1:39415"
key = "qwqowo" # api 密钥, 访问时需要提供
```

启动 !

## 使用

第一次启动时, 会要求输入手机号登录账号.

/add - 添加一个聊天进行索引, 会自动监听聊天的新消息

/del - 删除并取消监听聊天

可自定义是否监听以及是否监听消息删除事件

/unwatch - 不再监听一个聊天, 但不删除原先的索引数据

/watch - 监听一个聊天

/unwatchdel - 不监听一个聊天的消息删除事件

/watchdel - 监听一个聊天的消息删除事件

可创建子 bot , 子 bot 只有搜索功能且只能搜索指定的一些聊天, 这在为某些频道提供专属搜索功能时非常有用

/addsub - 添加一个子 bot

/delsub - 删除一个子 bot

/lssub - 列出所有子 bot
