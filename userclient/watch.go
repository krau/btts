package userclient

import (
	"fmt"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
)

func WatchHandler(ctx *ext.Context, u *ext.Update) error {
	if u.EffectiveMessage == nil || u.EffectiveMessage.Message == nil {
		return dispatcher.SkipCurrentGroup
	}
	if u.EffectiveMessage.IsService {
		return dispatcher.SkipCurrentGroup
	}

	log := log.FromContext(ctx)

	chatDB, err := database.GetIndexChat(ctx, u.EffectiveChat().GetID())
	if err != nil {
		log.Errorf("Failed to get chat: %v", err)
		return dispatcher.SkipCurrentGroup
	}
	if c := u.GetChannel(); c != nil {
		chatDB.ChatID = c.GetID()
		chatDB.Title = c.Title
		chatDB.Username = c.Username
		chatDB.Type = int(database.ChatTypeChannel)
	} else if c := u.GetChat(); c != nil {
		log.Debug("Not support group chat")
		return dispatcher.SkipCurrentGroup
	} else if c := u.GetUserChat(); c != nil {
		chatDB.ChatID = c.GetID()
		chatDB.Title = fmt.Sprintf("%s %s", c.FirstName, c.LastName)
		chatDB.Username = c.Username
		chatDB.Type = int(database.ChatTypePrivate)
	} else {
		log.Error("Chat is nil")
		return dispatcher.SkipCurrentGroup
	}
	// 那我问你 你不 watch 是怎么进来的
	chatDB.Watching = true
	if err := database.UpsertIndexChat(ctx, chatDB); err != nil {
		log.Warnf("Failed to upsert index chat: %v", err)
	}

	userDB := &database.UserInfo{}
	if u.EffectiveMessage.Out {
		userDB.ChatID = ctx.Self.ID
		userDB.Username = ctx.Self.Username
		userDB.FirstName = ctx.Self.FirstName
		userDB.LastName = ctx.Self.LastName
	} else {
		switch chatDB.Type {
		case int(database.ChatTypePrivate):
			userDB.ChatID = u.GetUserChat().GetID()
			userDB.Username = u.GetUserChat().Username
			userDB.FirstName = u.GetUserChat().FirstName
			userDB.LastName = u.GetUserChat().LastName
		case int(database.ChatTypeChannel):
			msg := u.EffectiveMessage
			if msg.Post {
				userDB.ChatID = u.GetChannel().GetID()
				userDB.Username = u.GetChannel().Username
				userDB.FirstName = u.GetChannel().Title
			} else {
				fromPeer := msg.FromID
				if fromPeer == nil {
					// 群组匿名身份
					userDB.ChatID = chatDB.ChatID
					userDB.Username = chatDB.Username
					userDB.FirstName = chatDB.Title
				} else {
					switch fromPeer := fromPeer.(type) {
					case *tg.PeerUser:
						userDB.ChatID = fromPeer.GetUserID()
						user, ok := u.Entities.Users[userDB.ChatID]
						if !ok {
							log.Warnf("User not found in entities: %d", userDB.ChatID)
							return dispatcher.SkipCurrentGroup
						}
						userDB.Username = user.Username
						userDB.FirstName = user.FirstName
						userDB.LastName = user.LastName
					case *tg.PeerChannel:
						userDB.ChatID = fromPeer.GetChannelID()
						user, ok := u.Entities.Channels[userDB.ChatID]
						if !ok {
							log.Warnf("Channel not found in entities: %d", userDB.ChatID)
							return dispatcher.SkipCurrentGroup
						}
						userDB.Username = user.Username
						userDB.FirstName = user.Title
					case *tg.PeerChat:
						userDB.ChatID = fromPeer.GetChatID()
						user, ok := u.Entities.Chats[userDB.ChatID]
						if !ok {
							log.Warnf("Chat not found in entities: %d", userDB.ChatID)
							return dispatcher.SkipCurrentGroup
						}
						userDB.FirstName = user.Title
					}
				}
			}
		}
	}

	if err := database.UpsertUserInfo(ctx, userDB); err != nil {
		log.Warnf("Failed to upsert user info: %v", err)
	}
	if slice.Contain(UC.GlobalIgnoreUsers, userDB.ChatID) {
		return dispatcher.SkipCurrentGroup
	}

	if err := engine.EgineInstance.AddDocumentsFromMessages(ctx, chatDB.ChatID, []*tg.Message{u.EffectiveMessage.Message}); err != nil {
		log.Errorf("Failed to add documents: %v", err)
	}
	return dispatcher.SkipCurrentGroup
}

func DeleteHandler(ctx *ext.Context, u *ext.Update) error {
	update, ok := u.UpdateClass.(*tg.UpdateDeleteChannelMessages)
	if !ok {
		return dispatcher.SkipCurrentGroup
	}
	chatID := update.GetChannelID()
	log := log.FromContext(ctx)
	chatDB, err := database.GetIndexChat(ctx, chatID)
	if err != nil {
		log.Errorf("Failed to get chat: %v", err)
		return dispatcher.SkipCurrentGroup
	}
	if chatDB.NoDelete {
		return dispatcher.SkipCurrentGroup
	}
	if err := engine.EgineInstance.DeleteDocuments(ctx, chatID, update.GetMessages()); err != nil {
		log.Errorf("Failed to delete documents: %v", err)
	}
	return dispatcher.SkipCurrentGroup
}
