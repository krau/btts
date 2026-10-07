package bot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/subbot"
	"github.com/krau/btts/types"
	"github.com/krau/btts/userclient"
	"github.com/krau/mygotg"
	"github.com/krau/mygotg/dispatcher"
	"github.com/krau/mygotg/storage"
)

const dispatchBotID = int64(700)
const dispatchUserID = int64(701)
const dispatchChannelID = int64(702)

type dispatchSearcher struct {
	engine.Searcher
	queries []string
}

func (s *dispatchSearcher) Search(_ context.Context, req types.SearchRequest) (*types.SearchResponse, error) {
	s.queries = append(s.queries, req.Query)
	return &types.SearchResponse{EstimatedTotalHits: 1, Hits: []types.SearchHit{{
		MessageDocument: types.MessageDocument{ID: 99, ChatID: dispatchChannelID, UserID: dispatchChannelID, Message: "matched message", Timestamp: 1},
		Formatted:       types.SearchHitFormatted{ChatID: "synthetic channel", Message: "matched message"},
	}}}, nil
}

type dispatchRPC struct {
	messages  map[int]*tg.Message
	sent      []*tg.Message
	callbacks int
	nextID    int
}

func (f *dispatchRPC) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	switch req := input.(type) {
	case *tg.BotsSetBotCommandsRequest:
		*output.(*tg.BoolBox) = tg.BoolBox{Bool: &tg.BoolTrue{}}
	case *tg.UpdatesGetDifferenceRequest:
		output.(*tg.UpdatesDifferenceBox).Difference = &tg.UpdatesDifferenceEmpty{Date: 1}
	case *tg.MessagesSendMessageRequest:
		f.nextID++
		var peer tg.PeerClass
		switch p := req.Peer.(type) {
		case *tg.InputPeerUser:
			peer = &tg.PeerUser{UserID: p.UserID}
		case *tg.InputPeerChannel:
			peer = &tg.PeerChannel{ChannelID: p.ChannelID}
		default:
			return fmt.Errorf("unexpected reply peer %T", p)
		}
		msg := &tg.Message{ID: f.nextID, Out: true, FromID: &tg.PeerUser{UserID: dispatchBotID}, PeerID: peer, Message: req.Message}
		if reply, ok := req.ReplyTo.(*tg.InputReplyToMessage); ok {
			msg.ReplyTo = &tg.MessageReplyHeader{ReplyToMsgID: reply.ReplyToMsgID}
		}
		f.messages[msg.ID] = msg
		f.sent = append(f.sent, msg)
		output.(*tg.UpdatesBox).Updates = &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewMessage{Message: msg}}, Users: dispatchUsers()}
	case *tg.MessagesGetMessagesRequest:
		var messages []tg.MessageClass
		for _, id := range req.ID {
			messages = append(messages, f.messages[id.(*tg.InputMessageID).ID])
		}
		output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: messages}
	case *tg.ChannelsGetMessagesRequest:
		var messages []tg.MessageClass
		for _, id := range req.ID {
			messages = append(messages, f.messages[id.(*tg.InputMessageID).ID])
		}
		output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesChannelMessages{Messages: messages}
	case *tg.MessagesSetBotCallbackAnswerRequest:
		f.callbacks++
		*output.(*tg.BoolBox) = tg.BoolBox{Bool: &tg.BoolTrue{}}
	default:
		return fmt.Errorf("unexpected synthetic RPC %T", input)
	}
	return nil
}

func dispatchUsers() []tg.UserClass {
	return []tg.UserClass{
		&tg.User{ID: dispatchBotID, AccessHash: 1700, Bot: true, Username: "synthetic_bot"},
		&tg.User{ID: dispatchUserID, AccessHash: 1701},
	}
}

func newDispatchClient(f *dispatchRPC) *mygotg.Client {
	peers := storage.NewPeerStorage(nil, true)
	disp := dispatcher.NewNativeDispatcher(true, false, nil, nil, peers)
	client := telegram.NewClient(1, "synthetic", telegram.Options{Middlewares: []telegram.Middleware{
		telegram.MiddlewareFunc(func(tg.Invoker) telegram.InvokeFunc { return f.Invoke }),
	}})
	self := dispatchUsers()[0].(*tg.User)
	disp.Initialize(context.Background(), func() {}, client, self)
	return &mygotg.Client{Client: client, Dispatcher: disp, PeerStorage: peers, Self: self}
}

func dispatchMessage(t *testing.T, client *mygotg.Client, f *dispatchRPC, msg *tg.Message, edited bool) {
	t.Helper()
	f.messages[msg.ID] = msg
	var update tg.UpdateClass
	if _, channel := msg.PeerID.(*tg.PeerChannel); channel {
		if edited {
			update = &tg.UpdateEditChannelMessage{Message: msg}
		} else {
			update = &tg.UpdateNewChannelMessage{Message: msg}
		}
	} else if edited {
		update = &tg.UpdateEditMessage{Message: msg}
	} else {
		update = &tg.UpdateNewMessage{Message: msg}
	}
	err := client.Dispatcher.Handle(context.Background(), &tg.Updates{
		Updates: []tg.UpdateClass{update}, Users: dispatchUsers(),
		Chats: []tg.ChatClass{&tg.Channel{ID: dispatchChannelID, AccessHash: 1702, Megagroup: true}},
	})
	if err != nil && !errors.Is(err, dispatcher.EndGroups) {
		t.Fatal(err)
	}
}

func TestBotDispatchDoesNotSearchOwnMessages(t *testing.T) {
	// Each run owns the production database singleton in a separate process.
	if os.Getenv("BTTS_DISPATCH_TEST_CHILD") != "1" {
		child := exec.Command(os.Args[0], "-test.run=^TestBotDispatchDoesNotSearchOwnMessages$")
		child.Env = append(os.Environ(), "BTTS_DISPATCH_TEST_CHILD=1")
		if output, err := child.CombinedOutput(); err != nil {
			t.Fatalf("isolated message dispatch: %v\n%s", err, output)
		}
		return
	}
	t.Chdir(t.TempDir())
	if err := os.Mkdir("data", 0700); err != nil {
		t.Fatal(err)
	}
	if err := database.InitDatabase(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertIndexChat(context.Background(), &database.IndexChat{ChatID: dispatchChannelID, Type: int(database.ChatTypeChannel)}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertSubBot(context.Background(), &database.SubBot{BotID: dispatchBotID}); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{"main", "subbot"} {
		t.Run(target, func(t *testing.T) {
			f := &dispatchRPC{messages: make(map[int]*tg.Message), nextID: 1000}
			client := newDispatchClient(f)
			searcher := &dispatchSearcher{}
			old := bi
			t.Cleanup(func() { bi = old })
			bi = &Bot{Client: client, UserClient: &userclient.UserClient{TClient: &mygotg.Client{Self: &tg.User{ID: dispatchUserID}}}, Engine: searcher}
			if target == "main" {
				bi.RegisterHandlers(context.Background())
			} else {
				(&subbot.SubBot{Client: client, ID: dispatchBotID}).Start()
			}

			incoming := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: dispatchUserID}, FromID: &tg.PeerUser{UserID: dispatchUserID}, Message: "first query"}
			dispatchMessage(t, client, f, incoming, false)
			if len(f.sent) != 1 {
				t.Fatalf("incoming query replies=%d, want 1", len(f.sent))
			}
			firstReply := f.sent[0]
			dispatchMessage(t, client, f, firstReply, false)
			dispatchMessage(t, client, f, firstReply, true)
			selfSender := *firstReply
			selfSender.Out = false
			dispatchMessage(t, client, f, &selfSender, false)
			flaggedOutgoing := *firstReply
			flaggedOutgoing.FromID = nil
			dispatchMessage(t, client, f, &flaggedOutgoing, false)
			if len(f.sent) != 1 {
				t.Fatalf("Bot's own reply retriggered search: replies=%d, want 1", len(f.sent))
			}

			for i, text := range []string{"/search own output", "/help", "/start"} {
				dispatchMessage(t, client, f, &tg.Message{ID: 10 + i, Out: true, FromID: &tg.PeerUser{UserID: dispatchBotID}, PeerID: incoming.PeerID, Message: text}, false)
			}
			if len(f.sent) != 1 {
				t.Fatalf("outgoing commands produced replies: %d", len(f.sent))
			}

			incoming = &tg.Message{ID: 20, PeerID: incoming.PeerID, FromID: incoming.FromID, Message: "/search second query"}
			dispatchMessage(t, client, f, incoming, false)
			incoming = &tg.Message{ID: 21, PeerID: incoming.PeerID, FromID: incoming.FromID, Message: "reply query", ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: firstReply.ID}}
			dispatchMessage(t, client, f, incoming, false)
			if len(f.sent) != 3 {
				t.Fatalf("normal command/reply queries broken: replies=%d, want 3", len(f.sent))
			}

			groupPeer := &tg.PeerChannel{ChannelID: dispatchChannelID}
			groupReply := &tg.Message{ID: 30, Out: true, FromID: &tg.PeerUser{UserID: dispatchBotID}, PeerID: groupPeer, Message: "group result"}
			dispatchMessage(t, client, f, groupReply, false)
			dispatchMessage(t, client, f, groupReply, true)
			selfReply := *groupReply
			selfReply.ID = 33
			selfReply.ReplyTo = &tg.MessageReplyHeader{ReplyToMsgID: groupReply.ID}
			dispatchMessage(t, client, f, &selfReply, false)
			channelPost := *groupReply
			channelPost.ID, channelPost.Post = 34, true
			channelPost.FromID = &tg.PeerChannel{ChannelID: dispatchChannelID}
			channelPost.Message = "/search bot channel post"
			dispatchMessage(t, client, f, &channelPost, false)
			dispatchMessage(t, client, f, &tg.Message{ID: 31, PeerID: groupPeer, FromID: incoming.FromID, Message: "group reply query", ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: groupReply.ID}}, false)
			if len(f.sent) != 4 {
				t.Fatalf("user reply to Bot in group broken: replies=%d, want 4", len(f.sent))
			}
			dispatchMessage(t, client, f, &tg.Message{ID: 32, PeerID: groupPeer, FromID: incoming.FromID, Message: "ordinary group chat"}, false)
			if len(f.sent) != 4 {
				t.Fatal("ordinary group message triggered search")
			}

			if target == "main" && len(searcher.queries) != 4 {
				t.Fatalf("self messages affected search count: got %d, want 4", len(searcher.queries))
			}
			err := client.Dispatcher.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateBotCallbackQuery{QueryID: 10, UserID: dispatchUserID, Peer: &tg.PeerUser{UserID: dispatchUserID}, Data: []byte("search 1 expired")}}, Users: dispatchUsers()})
			if err != nil && !errors.Is(err, dispatcher.EndGroups) {
				t.Fatal(err)
			}
			if f.callbacks != 1 {
				t.Fatal("button callback was blocked")
			}
		})
	}
}
