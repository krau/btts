package bot

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
)

func AddHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	args := update.Args()
	if len(args) < 2 {
		ctx.Reply(update, ext.ReplyTextString("Usage: /add <chat>"), nil)
		return dispatcher.EndGroups
	}
	log := log.FromContext(ctx)

	chatArg := args[1]
	var inputPeer tg.InputPeerClass
	utclient := bi.UserClient.TClient

	chatId, err := strconv.ParseInt(chatArg, 10, 64)
	if err != nil {
		effChat, err := utclient.CreateContext().ResolveUsername(strings.TrimPrefix(chatArg, "@"))
		if err != nil {
			ctx.Reply(update, ext.ReplyTextString("Failed to resolve username: "+err.Error()), nil)
			return dispatcher.EndGroups
		}
		inputPeer = effChat.GetInputPeer()
		chatId = effChat.GetID()
	} else {
		inputPeer = utclient.PeerStorage.GetInputPeerById(chatId)
	}
	if inputPeer == nil || chatId == 0 {
		ctx.Reply(update, ext.ReplyTextString("Chat not found"), nil)
		return dispatcher.EndGroups
	}

	_, err = database.GetIndexChat(ctx, chatId)
	if err == nil {
		ctx.Reply(update, ext.ReplyTextString("Chat already indexed"), nil)
		return dispatcher.EndGroups
	}

	if err := bi.Engine.CreateIndex(ctx, chatId); err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to create index: "+err.Error()), nil)
		return dispatcher.EndGroups
	}

	var gerr error
	defer func() {
		if gerr != nil {
			if err := bi.Engine.DeleteIndex(ctx, chatId); err != nil {
				log.Errorf("Failed to delete index: %v", err)
			}
			if err := database.DeleteIndexChat(ctx, chatId); err != nil {
				log.Errorf("Failed to delete index chat: %v", err)
			}
		}
	}()

	indexChat := &database.IndexChat{
		ChatID:   chatId,
		Watching: true,
		Public:   false,
	}

	switch inp := inputPeer.(type) {
	case *tg.InputPeerChannel:
		indexChat.Type = int(database.ChatTypeChannel)
	case *tg.InputPeerUser:
		indexChat.Type = int(database.ChatTypePrivate)
	default:
		log.Warnf("Unsupported chat type: %T", inp)
		ctx.Reply(update, ext.ReplyTextString("Unsupported chat type"), nil)
		return dispatcher.EndGroups
	}

	if gerr = database.UpsertIndexChat(ctx, indexChat); gerr != nil {
		log.Errorf("Failed to upsert index chat: %v", gerr)
		ctx.Reply(update, ext.ReplyTextString("Failed to add chat"), nil)
		return dispatcher.EndGroups
	}

	log.Infof("Adding chat: %s", chatArg)

	queryHistoryBuilder := query.Messages(utclient.API()).GetHistory(inputPeer).BatchSize(100)
	total, err := queryHistoryBuilder.Count(ctx)
	if err != nil {
		total = -1
		ctx.Reply(update, ext.ReplyTextString("Failed to count messages: "+err.Error()), nil)
	}

	ctx.Reply(update, ext.ReplyTextString("Total messages: "+strconv.Itoa(total)), nil)

	messageBatch := make([]*tg.Message, 0, 100)
	iter := queryHistoryBuilder.Iter()
	processed := 0
	for iter.Next(ctx) {
		value := iter.Value()
		msg := value.Msg
		switch msg := msg.(type) {
		case *tg.Message:
			messageBatch = append(messageBatch, msg)
			if len(messageBatch) >= 100 {
				log.Debugf("Adding batch of messages %d/%d", processed, total)
				docs := engine.DocumentsFromMessages(ctx, messageBatch, chatId, utclient.Self.ID, bi.UserClient.GetContext(), false)
				if err := bi.Engine.AddDocuments(ctx, chatId, docs); err != nil {
					log.Errorf("Failed to add documents: %v", err)
				}
				messageBatch = messageBatch[:0]
			}
		default:
			log.Warnf("Unsupported message type: %T", msg)
		}
		processed++
	}
	if err := iter.Err(); err != nil {
		gerr = err
		ctx.Reply(update, ext.ReplyTextString("Error: "+err.Error()), nil)
		return dispatcher.EndGroups
	}
	if len(messageBatch) > 0 {
		log.Debugf("Adding final batch of messages %d/%d", processed, total)
		docs := engine.DocumentsFromMessages(ctx, messageBatch, chatId, utclient.Self.ID, bi.UserClient.GetContext(), false)
		if err := bi.Engine.AddDocuments(ctx, chatId, docs); err != nil {
			log.Errorf("Failed to add documents: %v", err)
		}
	}

	ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf(
		"Added %d messages from chat %s", processed, chatArg,
	)), nil)
	return dispatcher.EndGroups
}
