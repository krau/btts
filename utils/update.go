package utils

import (
	"github.com/gotd/td/tg"
	"github.com/krau/mygotg/dispatcher"
	"github.com/krau/mygotg/ext"
)

// IgnoreBotMessages prevents outgoing replies from re-entering Bot commands and searches.
func IgnoreBotMessages(ctx *ext.Context, u *ext.Update) error {
	m := u.EffectiveMessage
	if m == nil || m.Message == nil {
		return dispatcher.ContinueGroups
	}
	if m.Out {
		return dispatcher.EndGroups
	}
	if sender, ok := m.FromID.(*tg.PeerUser); ok && sender.UserID == ctx.Self.ID {
		return dispatcher.EndGroups
	}
	return dispatcher.ContinueGroups
}

func GetUpdatePeerUser(u *ext.Update) *tg.PeerUser {
	if u.Entities == nil {
		return nil
	}
	var (
		peer tg.PeerClass
	)
	switch {
	case u.EffectiveMessage != nil:
		peer = u.EffectiveMessage.PeerID
	case u.CallbackQuery != nil:
		peer = u.CallbackQuery.Peer
	case u.ChatJoinRequest != nil:
		peer = u.ChatJoinRequest.Peer
	case u.ChatParticipant != nil:
		peer = &tg.PeerChat{ChatID: u.ChatParticipant.ChatID}
	}
	if peer == nil {
		return nil
	}
	c, ok := peer.(*tg.PeerUser)
	if !ok {
		return nil
	}
	return c
}

func GetUpdateChatID(u *ext.Update) int64 {
	if id := u.EffectiveChat().GetID(); id != 0 {
		return id
	}
	if u.Entities == nil || !u.Entities.Short {
		return 0
	}
	pu := GetUpdatePeerUser(u)
	if pu == nil {
		return 0
	}
	return pu.GetUserID()
}
