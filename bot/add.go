package bot

import (
	"strconv"
	"strings"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
)

func AddHandler(ctx *ext.Context, update *ext.Update) error {
	args := update.Args()
	if len(args) < 2 {
		ctx.Reply(update, ext.ReplyTextString("Usage: /add <chat>"), nil)
		return dispatcher.EndGroups
	}
	chatArg := args[1]
	var inputPeer tg.InputPeerClass
	utclient := BotInstance.UserClient.TClient

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

	if err := database.UpsertIndexChat(ctx, indexChat); err != nil {
		log.Errorf("Failed to upsert index chat: %v", err)
		ctx.Reply(update, ext.ReplyTextString("Failed to add chat"), nil)
		return dispatcher.EndGroups
	}

	log := log.FromContext(ctx)
	log.Infof("Adding chat: %s", chatArg)

	queryHistoryBuilder := query.Messages(utclient.API()).GetHistory(inputPeer).BatchSize(100)
	total, err := queryHistoryBuilder.Count(ctx)

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
			processed++
			if len(messageBatch) >= 100 {
				log.Debugf("Adding batch of messages %d/%d", processed, total)
				if err := BotInstance.Engine.AddDocumentsFromMessages(ctx, chatId, messageBatch); err != nil {
					log.Errorf("Failed to add documents: %v", err)
				}
				messageBatch = messageBatch[:0]
			}
		default:
			log.Warnf("Unsupported message type: %T", msg)
		}
	}
	if err := iter.Err(); err != nil {
		ctx.Reply(update, ext.ReplyTextString("Error: "+err.Error()), nil)
		return dispatcher.EndGroups
	}
	if len(messageBatch) > 0 {
		log.Debugf("Adding final batch of messages %d/%d", processed, total)
		if err := BotInstance.Engine.AddDocumentsFromMessages(ctx, chatId, messageBatch); err != nil {
			log.Errorf("Failed to add documents: %v", err)
		}
	}

	ctx.Reply(update, ext.ReplyTextString("Added chat: "+chatArg), nil)
	return dispatcher.EndGroups
}
