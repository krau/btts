package bot

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/utils"
)

func DownloadHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	chatDB, err := utils.GetChatDBFromUpdateArgs(ctx, update)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("Usage: /dl <chat_id> <message_range>\n%s", err.Error())), nil)
		return dispatcher.EndGroups
	}
	if len(update.Args()) < 3 {
		ctx.Reply(update, ext.ReplyTextString("Usage: /dl <chat_id> <message_range>"), nil)
		return dispatcher.EndGroups
	}
	messageRange := update.Args()[2]
	msgIds := strings.Split(messageRange, "-")
	if len(msgIds) != 2 {
		ctx.Reply(update, ext.ReplyTextString("Invalid message range"), nil)
		return dispatcher.EndGroups
	}
	startMsgID, err := strconv.Atoi(msgIds[0])
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Invalid start message ID"), nil)
		return dispatcher.EndGroups
	}
	endMsgID, err := strconv.Atoi(msgIds[1])
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Invalid end message ID"), nil)
		return dispatcher.EndGroups
	}
	if startMsgID > endMsgID {
		ctx.Reply(update, ext.ReplyTextString("Start message ID cannot be greater than end message ID"), nil)
		return dispatcher.EndGroups
	}
	if startMsgID == endMsgID {
		ctx.Reply(update, ext.ReplyTextString("Start message ID cannot be equal to end message ID"), nil)
		return dispatcher.EndGroups
	}
	chatID := chatDB.ChatID

	uapi := bi.UserClient.TClient.API()
	upeer := bi.UserClient.TClient.PeerStorage
	inputPeer := upeer.GetInputPeerById(chatID)
	if inputPeer == nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to get input peer"), nil)
		return dispatcher.EndGroups
	}

	total := endMsgID - startMsgID
	processed := 0
	for i := 0; i < total; i += 100 {
		start := startMsgID + i
		end := min(start+100, endMsgID)

		msgs, err := uapi.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:      inputPeer,
			OffsetID:  start,
			AddOffset: start - end,
			Limit:     100,
		})
		if err != nil {
			ctx.Reply(update, ext.ReplyTextString("Failed to get messages: "+err.Error()), nil)
			return dispatcher.EndGroups
		}

		var msgClass []tg.MessageClass
		switch msgsv := msgs.(type) {
		case *tg.MessagesMessages:
			msgClass = msgsv.GetMessages()
		case *tg.MessagesMessagesSlice:
			msgClass = msgsv.GetMessages()
		case *tg.MessagesChannelMessages:
			msgClass = msgsv.GetMessages()
		default:
			log.FromContext(ctx).Errorf("Unsupported message type: %T", msgsv)
			continue
		}

		messageBatch := make([]*tg.Message, 0, 100)

		for _, msg := range msgClass {
			msgNotEmpty, ok := msg.AsNotEmpty()
			if !ok {
				continue
			}
			switch msgNotEmptyV := msgNotEmpty.(type) {
			case *tg.Message:
				messageBatch = append(messageBatch, msgNotEmptyV)
			default:
				log.FromContext(ctx).Warnf("Unsupported message type: %T", msgNotEmptyV)
			}
		}
		if len(messageBatch) > 0 {
			processed += len(messageBatch)
			log.FromContext(ctx).Debugf("Adding batch of messages %d/%d", processed, total)
			docs := engine.DocumentsFromMessages(ctx, messageBatch, bi.UserClient.TClient.Self.ID, bi.UserClient.GetContext())
			if err := bi.Engine.AddDocuments(ctx, chatID, docs); err != nil {
				log.FromContext(ctx).Errorf("Failed to add documents: %v", err)
			}
		}
	}
	ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("Downloaded %d messages from %d to %d", processed, startMsgID, endMsgID)), nil)
	return dispatcher.EndGroups
}
