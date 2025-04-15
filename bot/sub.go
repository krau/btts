package bot

import (
	"fmt"
	"strconv"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/krau/btts/database"
	"github.com/krau/btts/subbot"
)

func AddSubHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	args := update.Args()
	if len(args) < 3 {
		ctx.Reply(update, ext.ReplyTextString("Usage: /addsub <token> <chat_ids...>"), nil)
		return dispatcher.EndGroups
	}
	token := args[1]
	chatIDsArgs := args[2:]
	chatIDs := make([]int64, len(chatIDsArgs))
	for i, chatID := range chatIDsArgs {
		idInt, err := strconv.ParseInt(chatID, 10, 64)
		if err != nil {
			ctx.Reply(update, ext.ReplyTextString("Invalid chat ID: "+chatID), nil)
			return dispatcher.EndGroups
		}
		chatIDs[i] = idInt
	}
	sb, err := subbot.NewSubBot(ctx, token, chatIDs)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to create sub bot: "+err.Error()), nil)
		return dispatcher.EndGroups
	}
	sb.Start()
	ctx.Reply(update, ext.ReplyTextString("Sub bot started successfully"), nil)
	return dispatcher.EndGroups
}

func DelSubHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	args := update.Args()
	if len(args) < 2 {
		ctx.Reply(update, ext.ReplyTextString("Usage: /delsub <botid>"), nil)
		return dispatcher.EndGroups
	}
	botIDStr := args[1]
	botID, err := strconv.ParseInt(botIDStr, 10, 64)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Invalid bot ID: "+botIDStr), nil)
		return dispatcher.EndGroups
	}
	if err = subbot.DelSubBot(ctx, botID); err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to delete sub bot: "+err.Error()), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString("Sub bot stopped successfully"), nil)
	return dispatcher.EndGroups
}

func ListSubHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	sbs, err := database.GetAllSubBots(ctx)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to get sub bots: "+err.Error()), nil)
		return dispatcher.EndGroups
	}
	runningSbs := subbot.GetAll(ctx)
	if len(sbs) != len(runningSbs) {
		log.FromContext(ctx).Errorf("Sub bot count mismatch: %d != %d", len(sbs), len(runningSbs))
		ctx.Reply(update, ext.ReplyTextString("Sub bot count mismatch!!!"), nil)
		return dispatcher.EndGroups
	}
	sbListStyling := make([]styling.StyledTextOption, 0)
	sbListStyling = append(sbListStyling, styling.Bold(fmt.Sprintf("%d sub bots running:\n", len(sbs))))
	for _, sb := range runningSbs {
		botDb, err := database.GetSubBot(ctx, sb.ID)
		if err != nil {
			log.FromContext(ctx).Errorf("Failed to get sub bot from db: %v", err)
			ctx.Reply(update, ext.ReplyTextString("Failed to get sub bot from db: "+err.Error()), nil)
			return dispatcher.EndGroups
		}
		botChats := make([]*database.IndexChat, 0)
		for _, chatID := range botDb.ChatIDs {
			chat, err := database.GetIndexChat(ctx, chatID)
			if err != nil {
				log.FromContext(ctx).Errorf("Failed to get index chat from db: %v", err)
				ctx.Reply(update, ext.ReplyTextString("Failed to get index chat from db: "+err.Error()), nil)
				return dispatcher.EndGroups
			}
			botChats = append(botChats, chat)
		}
		sbListStyling = append(sbListStyling, styling.Plain(fmt.Sprintf("\n@%s - ", sb.Name)))
		sbListStyling = append(sbListStyling, styling.Code(fmt.Sprintf("%d", sb.ID)))
		for _, bc := range botChats {
			sbListStyling = append(sbListStyling, styling.Plain(fmt.Sprintf("\n%s - ", bc.Title)))
			sbListStyling = append(sbListStyling, styling.Code(fmt.Sprintf("%d", bc.ChatID)))
		}
	}
	ctx.Reply(update, ext.ReplyTextStyledTextArray(sbListStyling), nil)
	return dispatcher.EndGroups
}
