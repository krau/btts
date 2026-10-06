package plugin

import (
	"sync"

	"github.com/charmbracelet/log"
	"github.com/krau/mygotg/ext"
)

// The user client dispatcher handles every incoming update sequentially in a
// single goroutine (see mygotg/dispatcher.NativeDispatcher.Handle). Running
// plugin logic inline there means a single blocking request - a slow network
// call, or the `floodwait` middleware sleeping for a FLOOD_WAIT returned by
// Telegram - freezes the whole update pipeline. Once a plugin has been parked
// like that nothing (not even the other plugins) is handled again until the
// process is restarted.
//
// DispatchPlugin hands the update to a worker goroutine instead, so the
// dispatcher returns immediately. Work for the same chat is serialized with a
// per-chat lock, which keeps ordered, stateful plugins (e.g. the interactive
// clean_member session) behaving as before, while a stuck chat can no longer
// block the update loop or the other chats.
var (
	pluginLocksMu sync.Mutex
	pluginLocks   = make(map[int64]*sync.Mutex)
)

func lockForChat(chatID int64) *sync.Mutex {
	pluginLocksMu.Lock()
	defer pluginLocksMu.Unlock()
	mu, ok := pluginLocks[chatID]
	if !ok {
		mu = &sync.Mutex{}
		pluginLocks[chatID] = mu
	}
	return mu
}

// DispatchPlugin runs Dispatcher for the given update without blocking the
// caller. It never returns an error: failures are logged from the worker.
func DispatchPlugin(ctx *ext.Context, u *ext.Update) {
	if ctx == nil || u == nil || u.EffectiveMessage == nil {
		return
	}
	var chatID int64
	if chat := u.EffectiveChat(); chat != nil {
		chatID = chat.GetID()
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("Plugin dispatcher panic", "chat_id", chatID, "panic", r)
			}
		}()
		mu := lockForChat(chatID)
		mu.Lock()
		defer mu.Unlock()
		if err := Dispatcher(ctx, u); err != nil {
			log.FromContext(ctx).Error("Plugin dispatcher error", "error", err)
		}
	}()
}
