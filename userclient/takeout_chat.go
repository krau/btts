package userclient

import (
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/takeout"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/config"
	"github.com/krau/mygotg/functions"
)

// TakeoutChatPage is one page of a single-chat takeout export.
type TakeoutChatPage struct {
	Messages  []*tg.Message
	Range     int
	Cursor    int
	RangeDone bool
	// Download streams a message media file through the active takeout
	// session, so media exports use invokeWithTakeout as well.
	Download func(ctx context.Context, media tg.MessageMediaClass, w io.Writer) error
}

// TakeoutImportChat exports one chat through a Telegram takeout session.
// It can run inside the live bot process; unrelated requests keep using the
// normal client. Media downloads use the active takeout session via the
// Download callback on each page.
func (u *UserClient) TakeoutImportChat(
	ctx context.Context,
	inputPeer tg.InputPeerClass,
	chatID int64,
	startRange, startCursor int,
	handler func(context.Context, TakeoutChatPage) error,
) error {
	maxBytes := int64(config.C.Enrich.MaxDownloadMB) << 20
	if maxBytes <= 0 {
		maxBytes = 20 << 20
	}
	cfg := takeout.Config{Contacts: false, Files: true, FileMaxSize: maxBytes}
	switch inputPeer.(type) {
	case *tg.InputPeerUser:
		cfg.MessageUsers = true
	case *tg.InputPeerChat:
		cfg.MessageChats = true
	case *tg.InputPeerChannel:
		cfg.MessageMegagroups = true
		cfg.MessageChannels = true
	default:
		cfg.MessageUsers = true
		cfg.MessageChats = true
		cfg.MessageMegagroups = true
		cfg.MessageChannels = true
	}
	logger := log.FromContext(ctx)
	return takeout.Run(ctx, u.TClient.API().Invoker(), cfg, func(ctx context.Context, client *takeout.Client) error {
		var rangeVector tg.MessageRangeVector
		if err := client.Invoke(ctx, &tg.MessagesGetSplitRangesRequest{}, &rangeVector); err != nil {
			return fmt.Errorf("get split ranges: %w", err)
		}
		ranges := rangeVector.Elems
		if len(ranges) == 0 {
			return fmt.Errorf("no split ranges returned")
		}
		// The saved cursor is a range index; keep the order deterministic so a
		// resumed session addresses the same range.
		sort.Slice(ranges, func(i, j int) bool { return ranges[i].MinID < ranges[j].MinID })
		logger.Infof("Takeout chat %d: %d split ranges, starting at %d/%d", chatID, len(ranges), startRange, startCursor)
		for rangeIndex := startRange; rangeIndex < len(ranges); rangeIndex++ {
			offset := 0
			if rangeIndex == startRange {
				offset = startCursor
			}
			for {
				inner := &tg.MessagesGetHistoryRequest{
					Peer:      inputPeer,
					OffsetID:  offset,
					Limit:     100,
					AddOffset: 0,
				}
				req := &tg.InvokeWithMessagesRangeRequest{Range: ranges[rangeIndex], Query: inner}
				var result tg.MessagesMessagesBox
				if err := client.Invoke(ctx, req, &result); err != nil {
					return fmt.Errorf("get history range %d: %w", rangeIndex, err)
				}
				var raw []tg.MessageClass
				switch m := result.Messages.(type) {
				case *tg.MessagesMessages:
					raw = m.Messages
				case *tg.MessagesMessagesSlice:
					raw = m.Messages
				case *tg.MessagesChannelMessages:
					raw = m.Messages
				default:
					raw = nil
				}
				if len(raw) == 0 {
					if err := handler(ctx, TakeoutChatPage{Range: rangeIndex + 1, RangeDone: true}); err != nil {
						return err
					}
					break
				}
				regular := make([]*tg.Message, 0, len(raw))
				lastID := 0
				for _, item := range raw {
					switch msg := item.(type) {
					case *tg.Message:
						regular = append(regular, msg)
						lastID = msg.ID
					case *tg.MessageService:
						lastID = msg.ID
					case *tg.MessageEmpty:
						lastID = msg.ID
					}
				}
				rangeDone := len(raw) < 100
				page := TakeoutChatPage{
					Messages:  regular,
					Range:     rangeIndex,
					Cursor:    lastID,
					RangeDone: rangeDone,
					Download: func(ctx context.Context, media tg.MessageMediaClass, w io.Writer) error {
						return u.takeoutDownloadMedia(ctx, client, media, w)
					},
				}
				if rangeDone {
					page.Range = rangeIndex + 1
					page.Cursor = 0
				}
				if err := handler(ctx, page); err != nil {
					return err
				}
				if rangeDone {
					break
				}
				offset = lastID
			}
		}
		return nil
	})
}

type takeoutDownloaderClient struct {
	client *takeout.Client
}

func (c takeoutDownloaderClient) UploadGetFile(ctx context.Context, req *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	var result tg.UploadFileBox
	if err := c.client.Invoke(ctx, req, &result); err != nil {
		return nil, err
	}
	return result.File, nil
}

func (c takeoutDownloaderClient) UploadGetFileHashes(ctx context.Context, req *tg.UploadGetFileHashesRequest) ([]tg.FileHash, error) {
	var result tg.FileHashVector
	if err := c.client.Invoke(ctx, req, &result); err != nil {
		return nil, err
	}
	return result.Elems, nil
}

func (c takeoutDownloaderClient) UploadReuploadCDNFile(ctx context.Context, req *tg.UploadReuploadCDNFileRequest) ([]tg.FileHash, error) {
	var result tg.FileHashVector
	if err := c.client.Invoke(ctx, req, &result); err != nil {
		return nil, err
	}
	return result.Elems, nil
}

func (c takeoutDownloaderClient) UploadGetCDNFileHashes(ctx context.Context, req *tg.UploadGetCDNFileHashesRequest) ([]tg.FileHash, error) {
	var result tg.FileHashVector
	if err := c.client.Invoke(ctx, req, &result); err != nil {
		return nil, err
	}
	return result.Elems, nil
}

func (c takeoutDownloaderClient) UploadGetWebFile(ctx context.Context, req *tg.UploadGetWebFileRequest) (*tg.UploadWebFile, error) {
	var result tg.UploadWebFile
	if err := c.client.Invoke(ctx, req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (u *UserClient) takeoutDownloadMedia(ctx context.Context, client *takeout.Client, media tg.MessageMediaClass, w io.Writer) error {
	location, err := functions.GetInputFileLocation(media)
	if err != nil {
		return fmt.Errorf("resolve media location: %w", err)
	}
	_, err = downloader.NewDownloader().
		Download(takeoutDownloaderClient{client: client}, location).
		WithThreads(1).
		Stream(ctx, w)
	return err
}
