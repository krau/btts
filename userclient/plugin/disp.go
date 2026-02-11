package plugin

import (
	"strings"

	"github.com/krau/btts/config"
	"github.com/krau/mygotg/ext"
)

type PluginFunc func(ctx *Context, u *ext.Update) error

var commandPlugins = map[string]PluginFunc{
	"re": RepeatHandler,
}

func Dispatcher(ctx *ext.Context, u *ext.Update) error {
	if u == nil || u.EffectiveMessage == nil {
		return nil
	}
	if u.EffectiveMessage.Out {
		text := u.EffectiveMessage.GetMessage()
		matched, command, args := func() (bool, string, string) {
			for _, prefix := range config.C.Plugin.Prefixes {
				if strings.HasPrefix(text, prefix) {
					// example: ,echo hello world
					// command: echo
					// args: hello world
					parts := strings.SplitN(text[len(prefix):], " ", 2)
					cmd := parts[0]
					var args string
					if len(parts) > 1 {
						args = parts[1]
					}
					return true, cmd, args
				}
			}
			return false, "", ""
		}()
		if matched {
			if pluginFunc, ok := commandPlugins[command]; ok {
				pluginCtx := &Context{
					Context: ctx,
					Args:    strings.Fields(args),
					Cmd:     command,
				}
				return pluginFunc(pluginCtx, u)
			}
		}
	}
	return nil
}
