package bot

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"

	"github.com/google/uuid"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/krau/btts/database"
	"github.com/krau/mygotg/dispatcher"
	"github.com/krau/mygotg/ext"
)

// /addapikey <name> <key> <chat_ids...>
// 仅管理员可用，使用指定明文 key 创建子 API key
func AddApiKeyHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	args := update.Args()
	if len(args) < 4 {
		ctx.Reply(update, ext.ReplyTextString("Usage: /addapikey <name> <key> <chat_ids...>"), nil)
		return dispatcher.EndGroups
	}
	name := args[1]
	plainKey := args[2]
	chatIDsArgs := args[3:]
	chatIDs := make([]int64, 0, len(chatIDsArgs))
	for _, idStr := range chatIDsArgs {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			ctx.Reply(update, ext.ReplyTextString("Invalid chat ID: "+idStr), nil)
			return dispatcher.EndGroups
		}
		chatIDs = append(chatIDs, id)
	}
	sum := sha256.Sum256([]byte(plainKey))
	hash := hex.EncodeToString(sum[:])
	chats := make([]database.IndexChat, 0, len(chatIDs))
	for _, id := range chatIDs {
		chats = append(chats, database.IndexChat{ChatID: id})
	}
	apiKey := &database.ApiKey{
		Name:    name,
		KeyHash: hash,
		Chats:   chats,
	}
	if err := database.UpsertApiKey(ctx, apiKey); err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to save api key: "+err.Error()), nil)
		return dispatcher.EndGroups
	}
	st := []styling.StyledTextOption{
		styling.Bold("New API key created:\n"),
		styling.Plain("ID: "),
		styling.Code(strconv.FormatUint(uint64(apiKey.ID), 10)),
		styling.Plain("\nName: "),
		styling.Code(apiKey.Name),
		styling.Plain("\nKey: "), // 仅此处展示明文 key
		styling.Code(plainKey),
		styling.Plain("\nChats: "),
	}
	for i, id := range chatIDs {
		if i > 0 {
			st = append(st, styling.Plain(", "))
		}
		st = append(st, styling.Code(strconv.FormatInt(id, 10)))
	}
	ctx.Reply(update, ext.ReplyTextStyledTextArray(st), nil)
	return dispatcher.EndGroups
}

// /genapikey
func GenApiKeyHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	st := []styling.StyledTextOption{
		styling.Bold("Generated API key candidate:\n"),
		styling.Code(uuid.NewString()),
	}
	ctx.Reply(update, ext.ReplyTextStyledTextArray(st), nil)
	return dispatcher.EndGroups
}

// /delapikey <id>
func DelApiKeyHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	args := update.Args()
	if len(args) < 2 {
		ctx.Reply(update, ext.ReplyTextString("Usage: /delapikey <id>"), nil)
		return dispatcher.EndGroups
	}
	idVal, err := strconv.ParseUint(args[1], 10, 64)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Invalid api key id"), nil)
		return dispatcher.EndGroups
	}
	if err = database.DeleteApiKey(ctx, uint(idVal)); err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to delete api key: "+err.Error()), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString("Api key deleted"), nil)
	return dispatcher.EndGroups
}

// /lsapikey
func ListApiKeyHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	keys, err := database.GetAllApiKeys(ctx)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to list api keys: "+err.Error()), nil)
		return dispatcher.EndGroups
	}
	if len(keys) == 0 {
		ctx.Reply(update, ext.ReplyTextString("No api keys"), nil)
		return dispatcher.EndGroups
	}
	st := []styling.StyledTextOption{
		styling.Bold("API keys:\n"),
	}
	for _, k := range keys {
		st = append(st,
			styling.Plain("\nID: "), styling.Code(strconv.FormatUint(uint64(k.ID), 10)),
			styling.Plain(" Name: "), styling.Code(k.Name),
			styling.Plain(" Chats: "))
		chatIDs := k.ChatIDs()
		for i, id := range chatIDs {
			if i > 0 {
				st = append(st, styling.Plain(", "))
			}
			st = append(st, styling.Code(strconv.FormatInt(id, 10)))
		}
	}
	ctx.Reply(update, ext.ReplyTextStyledTextArray(st), nil)
	return dispatcher.EndGroups
}

// /setapikey <id> <chat_ids...>
func SetApiKeyHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	args := update.Args()
	if len(args) < 3 {
		ctx.Reply(update, ext.ReplyTextString("Usage: /setapikeychats <id> <chat_ids...>"), nil)
		return dispatcher.EndGroups
	}
	idVal, err := strconv.ParseUint(args[1], 10, 64)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Invalid api key id"), nil)
		return dispatcher.EndGroups
	}
	apiKey, err := database.GetApiKeyByID(ctx, uint(idVal))
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to get api key: "+err.Error()), nil)
		return dispatcher.EndGroups
	}
	chatIDsArgs := args[2:]
	chats := make([]database.IndexChat, 0, len(chatIDsArgs))
	for _, idStr := range chatIDsArgs {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			ctx.Reply(update, ext.ReplyTextString("Invalid chat ID: "+idStr), nil)
			return dispatcher.EndGroups
		}
		chats = append(chats, database.IndexChat{ChatID: id})
	}
	apiKey.Chats = chats
	if err := database.UpsertApiKey(ctx, apiKey); err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to update api key: "+err.Error()), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString("Api key chats updated"), nil)
	return dispatcher.EndGroups
}
