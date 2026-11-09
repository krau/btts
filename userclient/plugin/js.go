package plugin

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tdp"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/config"
	"github.com/krau/mygotg/ext"
)

const (
	jsRuntimePoolSize = 4
	jsMemoryLimit     = 64 << 20
	jsMaxSafeInteger  = float64(1 << 53)
)

type jsUpdate struct {
	ChatID    int64      `json:"chat_id"`
	MessageID int        `json:"message_id"`
	Text      string     `json:"text"`
	Out       bool       `json:"out"`
	SelfID    int64      `json:"self_id"`
	Cmd       string     `json:"cmd"`
	Args      []string   `json:"args"`
	ReplyTo   *jsMessage `json:"reply_to,omitempty"`
}

type jsMessage struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
}

type jsPlugin struct {
	name   string
	module *moejs.Module
	idle   chan *moejs.Runtime
}

type jsCommand struct {
	plugin *jsPlugin
	hook   moejs.Hook
}

var (
	jsLoadMu    sync.Mutex
	jsCommands  = map[string]*jsCommand{}
	jsCommandsM sync.RWMutex
)

// LoadJSPlugins compiles the plugins in the configured directory. Each
// exported function whose name does not start with "_" becomes a command.
func LoadJSPlugins() error {
	jsLoadMu.Lock()
	defer jsLoadMu.Unlock()

	dir := strings.TrimSpace(config.C.Plugin.Dir)
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			log.Warn("JS plugin directory not found", "dir", dir)
			return nil
		}
		return fmt.Errorf("read JS plugin directory: %w", err)
	}

	next := make(map[string]*jsCommand)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".js" && ext != ".mjs" && ext != ".ts" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		source, err := os.ReadFile(path)
		if err != nil {
			log.Error("Failed to read JS plugin", "file", path, "error", err)
			continue
		}
		var module *moejs.Module
		if ext == ".ts" {
			module, err = moejs.CompileTS(path, string(source))
		} else {
			module, err = moejs.Compile(path, string(source))
		}
		if err != nil {
			log.Error("Failed to compile JS plugin", "file", path, "error", err)
			continue
		}

		hooks := make(map[string]moejs.Hook)
		for _, name := range module.Exports() {
			if name == "default" || strings.HasPrefix(name, "_") {
				continue
			}
			hook, err := module.Hook(name)
			if err != nil {
				log.Debug("Skip non-function JS export", "file", path, "export", name, "error", err)
				continue
			}
			hooks[name] = hook
		}
		if len(hooks) == 0 {
			log.Warn("JS plugin has no command exports", "file", path)
			continue
		}

		plugin := &jsPlugin{
			name:   path,
			module: module,
			idle:   make(chan *moejs.Runtime, jsRuntimePoolSize),
		}
		rt := newJSRuntime()
		if err := rt.Load(module); err != nil {
			log.Error("Failed to load JS plugin", "file", path, "error", err)
			continue
		}
		plugin.idle <- rt

		loaded := make([]string, 0, len(hooks))
		for name, hook := range hooks {
			if _, exists := next[name]; exists {
				log.Error("Duplicate JS plugin command", "command", name, "file", path)
				continue
			}
			next[name] = &jsCommand{plugin: plugin, hook: hook}
			loaded = append(loaded, name)
		}
		if len(loaded) > 0 {
			log.Info("Loaded JS plugin", "file", path, "commands", loaded)
		}
	}

	jsCommandsM.Lock()
	jsCommands = next
	jsCommandsM.Unlock()
	return nil
}

func lookupJSCommand(name string) (*jsCommand, bool) {
	jsCommandsM.RLock()
	defer jsCommandsM.RUnlock()
	command, ok := jsCommands[name]
	return command, ok
}

func newJSRuntime() *moejs.Runtime {
	return moejs.NewRuntime(moejs.Options{MemoryLimit: jsMemoryLimit})
}

func (p *jsPlugin) acquire() (*moejs.Runtime, error) {
	select {
	case rt := <-p.idle:
		return rt, nil
	default:
	}
	rt := newJSRuntime()
	if err := rt.Load(p.module); err != nil {
		return nil, fmt.Errorf("load %s: %w", p.name, err)
	}
	return rt, nil
}

func (p *jsPlugin) release(rt *moejs.Runtime) {
	_ = rt.SetGlobal("host", nil)
	rt.ClearInterrupt()
	rt.ReleaseCallData()
	select {
	case p.idle <- rt:
	default:
	}
}

func (c *jsCommand) call(ctx *ext.Context, u *ext.Update, cmd string, args []string) error {
	rt, err := c.plugin.acquire()
	if err != nil {
		return err
	}
	defer c.plugin.release(rt)

	if err := rt.SetGlobal("host", jsHost(ctx)); err != nil {
		return fmt.Errorf("install host functions: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if args == nil {
		args = []string{}
	}
	arg, err := rt.FromGo(buildJSUpdate(ctx, u, cmd, args))
	if err != nil {
		return fmt.Errorf("convert update: %w", err)
	}
	return c.invoke(rt, arg, ctx)
}

func (c *jsCommand) invoke(rt *moejs.Runtime, arg moejs.Value, ctx context.Context) error {
	done := make(chan error, 1)
	go func() {
		res, err := rt.Call(c.hook, arg)
		if err != nil {
			done <- err
			return
		}
		if state, result, ok := moejs.PromiseResult(res); ok {
			switch state {
			case moejs.PromisePending:
				done <- errors.New("command returned a pending promise; commands must be synchronous")
				return
			case moejs.PromiseRejected:
				done <- fmt.Errorf("command rejected: %s", result.String())
				return
			}
		}
		done <- nil
	}()

	select {
	case err := <-done:
		return err
	default:
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		rt.Interrupt(ctx.Err())
		err := <-done
		if err != nil {
			return err
		}
		return ctx.Err()
	}
}

func buildJSUpdate(ctx *ext.Context, u *ext.Update, cmd string, args []string) jsUpdate {
	msg := u.EffectiveMessage
	update := jsUpdate{
		ChatID:    u.EffectiveChat().GetID(),
		MessageID: msg.GetID(),
		Text:      msg.GetMessage(),
		Out:       msg.Out,
		Cmd:       cmd,
		Args:      args,
	}
	if ctx.Self != nil {
		update.SelfID = ctx.Self.ID
	}
	if msg.ReplyToMessage != nil {
		update.ReplyTo = &jsMessage{
			ID:   msg.ReplyToMessage.GetID(),
			Text: msg.ReplyToMessage.GetMessage(),
		}
	}
	return update
}

func jsHost(ctx *ext.Context) map[string]any {
	return map[string]any{
		"editMessage":     jsEditMessage(ctx),
		"deleteMessages":  jsDeleteMessages(ctx),
		"forwardMessages": jsForwardMessages(ctx),
		"invoke":          jsInvoke(ctx),
		"call":            jsCall(ctx),
		"resolvePeer":     jsResolve(ctx, inputPeerToJS),
		"resolveUser":     jsResolve(ctx, inputPeerToInputUser),
		"resolveChannel":  jsResolve(ctx, inputPeerToInputChannel),
		"uploadFile":      jsUploadFile(ctx),
		"randomId":        jsRandomID,
		"methods":         jsMethods,
		"sleep":           jsSleep(ctx),
	}
}

func jsEditMessage(ctx *ext.Context) moejs.NativeFunc {
	return func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		chatID, err := jsInt64(r, moejs.Arg(args, 0))
		if err != nil {
			return moejs.Undefined(), fmt.Errorf("editMessage chat_id: %w", err)
		}
		messageID, err := jsInt(r, moejs.Arg(args, 1))
		if err != nil {
			return moejs.Undefined(), fmt.Errorf("editMessage message_id: %w", err)
		}
		text, err := jsString(r, moejs.Arg(args, 2))
		if err != nil {
			return moejs.Undefined(), fmt.Errorf("editMessage text: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return moejs.Undefined(), err
		}
		_, err = ctx.EditMessage(chatID, &tg.MessagesEditMessageRequest{
			ID:      messageID,
			Message: text,
		})
		return moejs.Undefined(), err
	}
}

func jsDeleteMessages(ctx *ext.Context) moejs.NativeFunc {
	return func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		chatID, err := jsInt64(r, moejs.Arg(args, 0))
		if err != nil {
			return moejs.Undefined(), fmt.Errorf("deleteMessages chat_id: %w", err)
		}
		ids, err := jsIntSlice(r, moejs.Arg(args, 1))
		if err != nil {
			return moejs.Undefined(), fmt.Errorf("deleteMessages ids: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return moejs.Undefined(), err
		}
		return moejs.Undefined(), ctx.DeleteMessages(chatID, ids)
	}
}

func jsForwardMessages(ctx *ext.Context) moejs.NativeFunc {
	return func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		fromChatID, err := jsInt64(r, moejs.Arg(args, 0))
		if err != nil {
			return moejs.Undefined(), fmt.Errorf("forwardMessages from_chat_id: %w", err)
		}
		toChatID, err := jsInt64(r, moejs.Arg(args, 1))
		if err != nil {
			return moejs.Undefined(), fmt.Errorf("forwardMessages to_chat_id: %w", err)
		}
		ids, err := jsIntSlice(r, moejs.Arg(args, 2))
		if err != nil {
			return moejs.Undefined(), fmt.Errorf("forwardMessages ids: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return moejs.Undefined(), err
		}
		_, err = ctx.ForwardMessages(fromChatID, toChatID, &tg.MessagesForwardMessagesRequest{ID: ids})
		return moejs.Undefined(), err
	}
}

// jsInvoke exposes the MTProto method registry:
//
//	host.invoke(`{"@type":"messages.sendMessage", ...}`)
func jsInvoke(ctx *ext.Context) moejs.NativeFunc {
	return func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		text, err := jsString(r, moejs.Arg(args, 0))
		if err != nil {
			return moejs.Undefined(), fmt.Errorf("invoke: %w", err)
		}
		params, err := parseJSONParams(text)
		if err != nil {
			return moejs.Undefined(), fmt.Errorf("invoke: %w", err)
		}
		return invokeMethod(ctx, r, params)
	}
}

// jsCall is the object form of jsInvoke:
//
//	host.call("messages.sendMessage", {peer, message, random_id})
//
// The method name becomes the top-level @type; nested objects keep their own.
func jsCall(ctx *ext.Context) moejs.NativeFunc {
	return func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		method, err := jsString(r, moejs.Arg(args, 0))
		if err != nil {
			return moejs.Undefined(), fmt.Errorf("call method: %w", err)
		}
		params := map[string]any{}
		if len(args) > 1 {
			value, err := r.ToGoStrict(moejs.Arg(args, 1))
			if err != nil {
				return moejs.Undefined(), fmt.Errorf("call params: %w", err)
			}
			if value != nil {
				object, ok := value.(map[string]any)
				if !ok {
					return moejs.Undefined(), errors.New("call params: expected an object")
				}
				params = object
			}
		}
		params["@type"] = method
		stripJSONNulls(params)
		return invokeMethod(ctx, r, params)
	}
}

func invokeMethod(ctx *ext.Context, r *moejs.Realm, params map[string]any) (moejs.Value, error) {
	method, ok := params["@type"].(string)
	if !ok || method == "" {
		return moejs.Undefined(), errors.New("missing @type method name")
	}
	if err := ctx.Err(); err != nil {
		return moejs.Undefined(), err
	}
	goMethod, ok := tdMethod(method)
	if !ok {
		return moejs.Undefined(), fmt.Errorf("unknown method %q", method)
	}
	value := reflect.ValueOf(ctx.Raw).MethodByName(goMethod)
	if !value.IsValid() {
		return moejs.Undefined(), fmt.Errorf("unknown method %q", method)
	}
	callArgs, err := methodArgs(ctx, method, value.Type(), params)
	if err != nil {
		return moejs.Undefined(), err
	}

	var results []reflect.Value
	var panicValue any
	func() {
		defer func() { panicValue = recover() }()
		results = value.Call(callArgs)
	}()
	if panicValue != nil {
		return moejs.Undefined(), fmt.Errorf("%s: %v", method, panicValue)
	}

	errorType := reflect.TypeOf((*error)(nil)).Elem()
	if last := len(results) - 1; last >= 0 && results[last].Type() == errorType {
		if !results[last].IsNil() {
			return moejs.Undefined(), results[last].Interface().(error)
		}
		results = results[:last]
	}
	switch len(results) {
	case 0:
		return moejs.Undefined(), nil
	case 1:
		return r.FromGo(tdToGo(results[0]))
	default:
		values := make([]any, len(results))
		for i, result := range results {
			values[i] = tdToGo(result)
		}
		return r.FromGo(values)
	}
}

func methodArgs(ctx context.Context, method string, methodType reflect.Type, params map[string]any) ([]reflect.Value, error) {
	contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
	if methodType.NumIn() == 0 || methodType.In(0) != contextType {
		return nil, fmt.Errorf("%s: unexpected method signature", method)
	}
	args := []reflect.Value{reflect.ValueOf(ctx)}
	if methodType.NumIn() == 1 {
		return args, nil
	}
	if methodType.NumIn() == 2 && isRequestType(methodType.In(1)) {
		request := reflect.New(methodType.In(1).Elem())
		if err := fillTDObject(request, params); err != nil {
			return nil, fmt.Errorf("%s: %w", method, err)
		}
		return append(args, request), nil
	}

	fields, err := methodFields(method)
	if err != nil {
		return nil, err
	}
	fieldIndex := 0
	for i := 1; i < methodType.NumIn(); i++ {
		argType := methodType.In(i)
		var selected *tdp.Field
		for fieldIndex < len(fields) {
			field := fields[fieldIndex]
			fieldIndex++
			fieldType, err := tdFieldType(method, field.Name)
			if err != nil {
				return nil, err
			}
			if !fieldType.AssignableTo(argType) && !fieldType.ConvertibleTo(argType) {
				continue
			}
			_, exists := lookupTDParam(params, field)
			if !exists && field.Null {
				continue
			}
			selected = &field
			break
		}
		if selected == nil {
			return nil, fmt.Errorf("%s: no value for argument %d", method, i)
		}
		value, exists := lookupTDParam(params, *selected)
		if !exists {
			return nil, fmt.Errorf("%s: missing %s", method, selected.SchemaName)
		}
		converted, err := goValueForType(value, argType)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", method, selected.SchemaName, err)
		}
		args = append(args, converted)
	}
	return args, nil
}

func tlMethodName(method string) string {
	parts := strings.Split(method, ".")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, "")
}

func parseJSONParams(text string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	value = convertJSONNumbers(value)
	params, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("expected a JSON object")
	}
	return params, nil
}

// jsResolve turns a numeric peer id or a username into the input type built by
// convert, so plugins do not have to handle access hashes themselves.
func jsResolve(ctx *ext.Context, convert func(tg.InputPeerClass) (map[string]any, error)) moejs.NativeFunc {
	return func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		peer, err := resolveInputPeer(ctx, r, moejs.Arg(args, 0))
		if err != nil {
			return moejs.Undefined(), err
		}
		object, err := convert(peer)
		if err != nil {
			return moejs.Undefined(), err
		}
		return r.FromGo(object)
	}
}

func resolveInputPeer(ctx *ext.Context, r *moejs.Realm, value moejs.Value) (tg.InputPeerClass, error) {
	if value.IsString() {
		username, err := jsString(r, value)
		if err != nil {
			return nil, err
		}
		chat, err := ctx.ResolveUsername(username)
		if err != nil {
			return nil, fmt.Errorf("resolve username %q: %w", username, err)
		}
		peer := chat.GetInputPeer()
		if peer == nil {
			return nil, fmt.Errorf("username %q has no input peer", username)
		}
		return peer, nil
	}
	id, err := jsInt64(r, value)
	if err != nil {
		return nil, fmt.Errorf("peer id: %w", err)
	}
	if ctx.Self != nil && id == ctx.Self.ID {
		return &tg.InputPeerSelf{}, nil
	}
	peer, err := ctx.ResolveInputPeerById(id)
	if err != nil {
		return nil, fmt.Errorf("resolve peer %d: %w", id, err)
	}
	return peer, nil
}

func inputPeerToJS(peer tg.InputPeerClass) (map[string]any, error) {
	switch peer := peer.(type) {
	case *tg.InputPeerEmpty:
		return map[string]any{"@type": "inputPeerEmpty"}, nil
	case *tg.InputPeerSelf:
		return map[string]any{"@type": "inputPeerSelf"}, nil
	case *tg.InputPeerChat:
		return map[string]any{"@type": "inputPeerChat", "chat_id": peer.ChatID}, nil
	case *tg.InputPeerUser:
		return map[string]any{
			"@type":       "inputPeerUser",
			"user_id":     peer.UserID,
			"access_hash": big.NewInt(peer.AccessHash),
		}, nil
	case *tg.InputPeerChannel:
		return map[string]any{
			"@type":       "inputPeerChannel",
			"channel_id":  peer.ChannelID,
			"access_hash": big.NewInt(peer.AccessHash),
		}, nil
	case *tg.InputPeerUserFromMessage:
		inner, err := inputPeerToJS(peer.Peer)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"@type":   "inputPeerUserFromMessage",
			"peer":    inner,
			"msg_id":  peer.MsgID,
			"user_id": peer.UserID,
		}, nil
	case *tg.InputPeerChannelFromMessage:
		inner, err := inputPeerToJS(peer.Peer)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"@type":      "inputPeerChannelFromMessage",
			"peer":       inner,
			"msg_id":     peer.MsgID,
			"channel_id": peer.ChannelID,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported input peer %T", peer)
	}
}

func inputPeerToInputUser(peer tg.InputPeerClass) (map[string]any, error) {
	switch peer := peer.(type) {
	case *tg.InputPeerEmpty:
		return map[string]any{"@type": "inputUserEmpty"}, nil
	case *tg.InputPeerSelf:
		return map[string]any{"@type": "inputUserSelf"}, nil
	case *tg.InputPeerUser:
		return map[string]any{
			"@type":       "inputUser",
			"user_id":     peer.UserID,
			"access_hash": big.NewInt(peer.AccessHash),
		}, nil
	case *tg.InputPeerUserFromMessage:
		inner, err := inputPeerToJS(peer.Peer)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"@type":   "inputUserFromMessage",
			"peer":    inner,
			"msg_id":  peer.MsgID,
			"user_id": peer.UserID,
		}, nil
	default:
		return nil, fmt.Errorf("peer %T is not a user", peer)
	}
}

func inputPeerToInputChannel(peer tg.InputPeerClass) (map[string]any, error) {
	switch peer := peer.(type) {
	case *tg.InputPeerEmpty:
		return map[string]any{"@type": "inputChannelEmpty"}, nil
	case *tg.InputPeerChannel:
		return map[string]any{
			"@type":       "inputChannel",
			"channel_id":  peer.ChannelID,
			"access_hash": big.NewInt(peer.AccessHash),
		}, nil
	case *tg.InputPeerChannelFromMessage:
		inner, err := inputPeerToJS(peer.Peer)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"@type":      "inputChannelFromMessage",
			"peer":       inner,
			"msg_id":     peer.MsgID,
			"channel_id": peer.ChannelID,
		}, nil
	default:
		return nil, fmt.Errorf("peer %T is not a channel", peer)
	}
}

func jsUploadFile(ctx *ext.Context) moejs.NativeFunc {
	return func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name, err := jsString(r, moejs.Arg(args, 0))
		if err != nil {
			return moejs.Undefined(), fmt.Errorf("uploadFile name: %w", err)
		}
		value, err := r.ToGoStrict(moejs.Arg(args, 1))
		if err != nil {
			return moejs.Undefined(), fmt.Errorf("uploadFile data: %w", err)
		}
		data, ok := value.([]byte)
		if !ok || data == nil {
			return moejs.Undefined(), errors.New("uploadFile data: expected an ArrayBuffer or Uint8Array")
		}
		if err := ctx.Err(); err != nil {
			return moejs.Undefined(), err
		}
		file, err := uploader.NewUploader(ctx.Raw).FromBytes(ctx, name, data)
		if err != nil {
			return moejs.Undefined(), err
		}
		object, err := inputFileToJS(file)
		if err != nil {
			return moejs.Undefined(), err
		}
		return r.FromGo(object)
	}
}

func inputFileToJS(file tg.InputFileClass) (map[string]any, error) {
	switch file := file.(type) {
	case *tg.InputFile:
		return map[string]any{
			"@type":        "inputFile",
			"id":           big.NewInt(file.ID),
			"parts":        file.Parts,
			"name":         file.Name,
			"md5_checksum": file.MD5Checksum,
		}, nil
	case *tg.InputFileBig:
		return map[string]any{
			"@type": "inputFileBig",
			"id":    big.NewInt(file.ID),
			"parts": file.Parts,
			"name":  file.Name,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported input file %T", file)
	}
}

// jsRandomID returns a positive int64 as a BigInt, suitable for random_id.
func jsRandomID(r *moejs.Realm, _ moejs.Value, _ []moejs.Value) (moejs.Value, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return moejs.Undefined(), err
	}
	id := int64(binary.LittleEndian.Uint64(b[:]) & (1<<63 - 1))
	return r.FromGo(big.NewInt(id))
}

// jsMethods returns the MTProto method names accepted by host.call/invoke.
func jsMethods(r *moejs.Realm, _ moejs.Value, _ []moejs.Value) (moejs.Value, error) {
	tdRegistry()
	return r.FromGo(tdMethodList)
}

// jsSleep blocks the current plugin call, for FloodWait backoff.
func jsSleep(ctx *ext.Context) moejs.NativeFunc {
	return func(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		ms, err := jsInt64(r, moejs.Arg(args, 0))
		if err != nil {
			return moejs.Undefined(), fmt.Errorf("sleep: %w", err)
		}
		if ms < 0 || ms > 300000 {
			return moejs.Undefined(), errors.New("sleep: ms must be between 0 and 300000")
		}
		if ms == 0 {
			return moejs.Undefined(), nil
		}
		timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return moejs.Undefined(), nil
		case <-ctx.Done():
			return moejs.Undefined(), ctx.Err()
		}
	}
}

func convertJSONNumbers(value any) any {
	switch value := value.(type) {
	case json.Number:
		return jsonNumberToGo(value)
	case map[string]any:
		for key, item := range value {
			value[key] = convertJSONNumbers(item)
		}
		return value
	case []any:
		for i, item := range value {
			value[i] = convertJSONNumbers(item)
		}
		return value
	default:
		return value
	}
}

func jsonNumberToGo(number json.Number) any {
	text := number.String()
	if !strings.ContainsAny(text, ".eE") {
		if integer, ok := new(big.Int).SetString(text, 10); ok {
			if !integer.IsInt64() {
				return integer
			}
			value := integer.Int64()
			if value > 1<<53 || value < -(1<<53) {
				return integer
			}
			return value
		}
	}
	if value, err := number.Float64(); err == nil {
		return value
	}
	return text
}

func stripJSONNulls(value any) {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			if item == nil {
				delete(value, key)
				continue
			}
			stripJSONNulls(item)
		}
	case []any:
		for _, item := range value {
			stripJSONNulls(item)
		}
	}
}

// The generic bridge fills generated request structs and calls the typed
// tg.Client method. gotd's InvokeJSON cannot be used here: it writes fields in
// TypeInfo order without the flags word, which breaks methods with flags.
// Reflection also keeps int64 access hashes exact and gives nested values
// their @type constructor name.
var (
	tdRegistryOnce sync.Once
	tdNames        map[string]uint32
	tdConstructors map[uint32]func() bin.Object
	tdMethods      map[string]string
	tdMethodList   []string
	tdObjectType   = reflect.TypeOf((*tdp.Object)(nil)).Elem()
	tdEncoderType  = reflect.TypeOf((*bin.Encoder)(nil)).Elem()
	binFieldsType  = reflect.TypeOf((*bin.Fields)(nil)).Elem()
)

func tdRegistry() (map[string]uint32, map[uint32]func() bin.Object) {
	tdRegistryOnce.Do(func() {
		tdNames = tg.NamesMap()
		tdConstructors = tg.TypesConstructorMap()
		buildTDMethodMap()
	})
	return tdNames, tdConstructors
}

func buildTDMethodMap() {
	client := reflect.TypeOf((*tg.Client)(nil))
	byNormalized := make(map[string]string, client.NumMethod())
	for i := range client.NumMethod() {
		method := client.Method(i)
		if method.Name == "InvokeJSON" || method.Name == "Invoker" {
			continue
		}
		byNormalized[normalizeTDName(method.Name)] = method.Name
	}
	tdMethods = make(map[string]string)
	for name := range tdNames {
		if !strings.Contains(name, ".") {
			continue
		}
		if goName, ok := byNormalized[normalizeTDName(tlMethodName(name))]; ok {
			tdMethods[name] = goName
		}
	}
	tdMethodList = make([]string, 0, len(tdMethods))
	for name := range tdMethods {
		tdMethodList = append(tdMethodList, name)
	}
	sort.Strings(tdMethodList)
}

func tdMethod(name string) (string, bool) {
	tdRegistry()
	if goName, ok := tdMethods[name]; ok {
		return goName, true
	}
	candidate := tlMethodName(name)
	if _, ok := reflect.TypeOf((*tg.Client)(nil)).MethodByName(candidate); ok {
		return candidate, true
	}
	return "", false
}

func isRequestType(t reflect.Type) bool {
	return t.Kind() == reflect.Ptr && t.Elem().Kind() == reflect.Struct &&
		t.Implements(tdObjectType) && t.Implements(tdEncoderType)
}

func methodFields(method string) ([]tdp.Field, error) {
	names, constructors := tdRegistry()
	id, ok := names[method]
	if !ok {
		return nil, fmt.Errorf("unknown method %q", method)
	}
	constructor, ok := constructors[id]
	if !ok {
		return nil, fmt.Errorf("no constructor for method %q", method)
	}
	object, ok := constructor().(tdp.Object)
	if !ok {
		return nil, fmt.Errorf("method %q has no type info", method)
	}
	return object.TypeInfo().Fields, nil
}

func tdFieldType(method, fieldName string) (reflect.Type, error) {
	names, constructors := tdRegistry()
	id, ok := names[method]
	if !ok {
		return nil, fmt.Errorf("unknown method %q", method)
	}
	constructor, ok := constructors[id]
	if !ok {
		return nil, fmt.Errorf("no constructor for method %q", method)
	}
	t := reflect.TypeOf(constructor())
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	field, ok := t.FieldByName(fieldName)
	if !ok {
		return nil, fmt.Errorf("method %q has no field %q", method, fieldName)
	}
	return field.Type, nil
}

func fillTDObject(target reflect.Value, params map[string]any) error {
	if target.Kind() != reflect.Ptr || target.IsNil() {
		return errors.New("target must be a non-nil pointer")
	}
	object, ok := target.Interface().(tdp.Object)
	if !ok {
		return fmt.Errorf("%s has no type info", target.Type())
	}
	fields := object.TypeInfo().Fields
	bySchema := make(map[string]tdp.Field, len(fields))
	byNormalized := make(map[string]tdp.Field, len(fields))
	for _, field := range fields {
		bySchema[field.SchemaName] = field
		byNormalized[normalizeTDName(field.SchemaName)] = field
	}

	for key, value := range params {
		if key == "@type" {
			continue
		}
		field, ok := bySchema[key]
		if !ok {
			field, ok = byNormalized[normalizeTDName(key)]
		}
		if !ok {
			return fmt.Errorf("unknown field %q", key)
		}
		targetField := target.Elem().FieldByName(field.Name)
		if !targetField.IsValid() {
			return fmt.Errorf("field %q has no Go representation", field.Name)
		}
		if err := setTDValue(targetField, value); err != nil {
			return fmt.Errorf("%s: %w", field.SchemaName, err)
		}
	}
	callSetFlags(target)
	return nil
}

func normalizeTDName(name string) string {
	return strings.ReplaceAll(strings.ToLower(name), "_", "")
}

func lookupTDParam(params map[string]any, field tdp.Field) (any, bool) {
	if value, ok := params[field.SchemaName]; ok {
		return value, true
	}
	if value, ok := params[field.Name]; ok {
		return value, true
	}
	normalized := normalizeTDName(field.SchemaName)
	for key, value := range params {
		if normalizeTDName(key) == normalized {
			return value, true
		}
	}
	return nil, false
}

func callSetFlags(value reflect.Value) {
	for _, name := range []string{"SetFlags", "SetFlags2"} {
		method := value.MethodByName(name)
		if method.IsValid() && method.Type().NumIn() == 0 {
			method.Call(nil)
		}
	}
}

func setTDValue(field reflect.Value, value any) error {
	if value == nil {
		return nil
	}
	if !field.CanSet() {
		return fmt.Errorf("field %s cannot be set", field.Type())
	}
	switch field.Kind() {
	case reflect.Interface:
		object, ok := value.(map[string]any)
		if !ok {
			if field.NumMethod() == 0 {
				source := reflect.ValueOf(value)
				if source.IsValid() && source.Type().AssignableTo(field.Type()) {
					field.Set(source)
					return nil
				}
			}
			return fmt.Errorf("expected an object with @type, got %T", value)
		}
		concrete, err := tdObjectFromMap(object)
		if err != nil {
			return err
		}
		if !concrete.Type().AssignableTo(field.Type()) {
			return fmt.Errorf("%s does not implement %s", concrete.Type(), field.Type())
		}
		field.Set(concrete)
		return nil
	case reflect.Ptr:
		pointer := reflect.New(field.Type().Elem())
		if err := setTDValue(pointer.Elem(), value); err != nil {
			return err
		}
		field.Set(pointer)
		return nil
	case reflect.Struct:
		if field.Type() == binFieldsType {
			return nil
		}
		if field.CanAddr() {
			if object, ok := field.Addr().Interface().(tdp.Object); ok {
				objectMap, ok := value.(map[string]any)
				if !ok {
					return fmt.Errorf("expected an object, got %T", value)
				}
				if typeName, _ := objectMap["@type"].(string); typeName != "" && typeName != object.TypeInfo().Name {
					return fmt.Errorf("expected @type %q, got %q", object.TypeInfo().Name, typeName)
				}
				return fillTDObject(field.Addr(), objectMap)
			}
		}
		return setBasicTDValue(field, value)
	case reflect.Slice:
		if field.Type().Elem().Kind() == reflect.Uint8 {
			return setTDByteSlice(field, value)
		}
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("expected an array, got %T", value)
		}
		out := reflect.MakeSlice(field.Type(), len(items), len(items))
		for i, item := range items {
			if err := setTDValue(out.Index(i), item); err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
		}
		field.Set(out)
		return nil
	case reflect.Array:
		if field.Type().Elem().Kind() == reflect.Uint8 {
			return setTDByteArray(field, value)
		}
		items, ok := value.([]any)
		if !ok || len(items) != field.Len() {
			return fmt.Errorf("expected an array of length %d", field.Len())
		}
		for i, item := range items {
			if err := setTDValue(field.Index(i), item); err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
		}
		return nil
	case reflect.Map:
		items, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("expected an object, got %T", value)
		}
		if field.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("unsupported map key %s", field.Type().Key())
		}
		out := reflect.MakeMapWithSize(field.Type(), len(items))
		for key, item := range items {
			mapped := reflect.New(field.Type().Elem()).Elem()
			if err := setTDValue(mapped, item); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			out.SetMapIndex(reflect.ValueOf(key).Convert(field.Type().Key()), mapped)
		}
		field.Set(out)
		return nil
	default:
		return setBasicTDValue(field, value)
	}
}

func goValueForType(value any, target reflect.Type) (reflect.Value, error) {
	result := reflect.New(target).Elem()
	if err := setTDValue(result, value); err != nil {
		return reflect.Value{}, err
	}
	return result, nil
}

func setBasicTDValue(field reflect.Value, value any) error {
	converted, err := basicTDValue(value, field.Type())
	if err != nil {
		return err
	}
	field.Set(converted)
	return nil
}

func basicTDValue(value any, target reflect.Type) (reflect.Value, error) {
	out := reflect.New(target).Elem()
	switch target.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		number, err := anyInt64(value)
		if err != nil {
			return reflect.Value{}, err
		}
		if target.Kind() == reflect.Int && (number < math.MinInt32 || number > math.MaxInt32) {
			return reflect.Value{}, fmt.Errorf("integer %d overflows TL int", number)
		}
		if out.OverflowInt(number) {
			return reflect.Value{}, fmt.Errorf("integer %d overflows %s", number, target)
		}
		out.SetInt(number)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		number, err := anyUint64(value)
		if err != nil {
			return reflect.Value{}, err
		}
		if target.Kind() == reflect.Uint && number > math.MaxUint32 {
			return reflect.Value{}, fmt.Errorf("integer %d overflows TL uint", number)
		}
		if out.OverflowUint(number) {
			return reflect.Value{}, fmt.Errorf("integer %d overflows %s", number, target)
		}
		out.SetUint(number)
	case reflect.Float32, reflect.Float64:
		number, err := anyFloat64(value)
		if err != nil {
			return reflect.Value{}, err
		}
		if out.OverflowFloat(number) {
			return reflect.Value{}, fmt.Errorf("float %v overflows %s", number, target)
		}
		out.SetFloat(number)
	case reflect.String:
		switch value := value.(type) {
		case string:
			out.SetString(value)
		case []byte:
			out.SetString(string(value))
		default:
			return reflect.Value{}, fmt.Errorf("expected a string, got %T", value)
		}
	case reflect.Bool:
		boolean, ok := value.(bool)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected a bool, got %T", value)
		}
		out.SetBool(boolean)
	default:
		source := reflect.ValueOf(value)
		if !source.IsValid() {
			return out, nil
		}
		if source.Type().AssignableTo(target) {
			return source, nil
		}
		if source.Type().ConvertibleTo(target) {
			return source.Convert(target), nil
		}
		return reflect.Value{}, fmt.Errorf("cannot convert %T to %s", value, target)
	}
	return out, nil
}

func anyInt64(value any) (int64, error) {
	if integer, ok := value.(*big.Int); ok {
		if !integer.IsInt64() {
			return 0, errors.New("integer out of range")
		}
		return integer.Int64(), nil
	}
	if number, ok := value.(json.Number); ok {
		if integer, err := number.Int64(); err == nil {
			return integer, nil
		}
	}
	source := reflect.ValueOf(value)
	if !source.IsValid() {
		return 0, errors.New("expected an integer")
	}
	switch source.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return source.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		number := source.Uint()
		if number > math.MaxInt64 {
			return 0, errors.New("integer out of range")
		}
		return int64(number), nil
	case reflect.Float32, reflect.Float64:
		number := source.Float()
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number ||
			number < math.MinInt64 || number > math.MaxInt64 {
			return 0, fmt.Errorf("expected an integer, got %v", number)
		}
		return int64(number), nil
	default:
		return 0, fmt.Errorf("expected an integer, got %T", value)
	}
}

func anyUint64(value any) (uint64, error) {
	if integer, ok := value.(*big.Int); ok {
		if !integer.IsUint64() {
			return 0, errors.New("integer out of range")
		}
		return integer.Uint64(), nil
	}
	if number, ok := value.(json.Number); ok {
		if integer, err := number.Int64(); err == nil && integer >= 0 {
			return uint64(integer), nil
		}
	}
	source := reflect.ValueOf(value)
	if !source.IsValid() {
		return 0, errors.New("expected an integer")
	}
	switch source.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		number := source.Int()
		if number < 0 {
			return 0, errors.New("integer out of range")
		}
		return uint64(number), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return source.Uint(), nil
	case reflect.Float32, reflect.Float64:
		number := source.Float()
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number ||
			number < 0 || number > math.MaxUint64 {
			return 0, fmt.Errorf("expected an unsigned integer, got %v", number)
		}
		return uint64(number), nil
	default:
		return 0, fmt.Errorf("expected an integer, got %T", value)
	}
}

func anyFloat64(value any) (float64, error) {
	if integer, ok := value.(*big.Int); ok {
		number, _ := new(big.Float).SetInt(integer).Float64()
		return number, nil
	}
	if number, ok := value.(json.Number); ok {
		return number.Float64()
	}
	source := reflect.ValueOf(value)
	if !source.IsValid() {
		return 0, errors.New("expected a number")
	}
	switch source.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(source.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(source.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return source.Float(), nil
	default:
		return 0, fmt.Errorf("expected a number, got %T", value)
	}
}

func setTDByteSlice(field reflect.Value, value any) error {
	bytes, err := tdBytes(value)
	if err != nil {
		return err
	}
	field.SetBytes(bytes)
	return nil
}

func setTDByteArray(field reflect.Value, value any) error {
	bytes, err := tdBytes(value)
	if err != nil {
		return err
	}
	if len(bytes) != field.Len() {
		return fmt.Errorf("expected %d bytes, got %d", field.Len(), len(bytes))
	}
	reflect.Copy(field, reflect.ValueOf(bytes))
	return nil
}

func tdBytes(value any) ([]byte, error) {
	switch value := value.(type) {
	case []byte:
		return value, nil
	case string:
		if len(value)%2 == 0 && isHex(value) {
			if decoded, err := hex.DecodeString(value); err == nil {
				return decoded, nil
			}
		}
		return []byte(value), nil
	case []any:
		bytes := make([]byte, len(value))
		for i, item := range value {
			number, err := anyInt64(item)
			if err != nil || number < 0 || number > 255 {
				return nil, fmt.Errorf("byte %d is invalid", i)
			}
			bytes[i] = byte(number)
		}
		return bytes, nil
	default:
		return nil, fmt.Errorf("expected bytes, got %T", value)
	}
}

func isHex(value string) bool {
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

func tdObjectFromMap(object map[string]any) (reflect.Value, error) {
	name, ok := object["@type"].(string)
	if !ok || name == "" {
		return reflect.Value{}, errors.New("missing @type")
	}
	names, constructors := tdRegistry()
	id, ok := names[name]
	if !ok {
		return reflect.Value{}, fmt.Errorf("unknown type %q", name)
	}
	constructor, ok := constructors[id]
	if !ok {
		return reflect.Value{}, fmt.Errorf("no constructor for type %q", name)
	}
	value := constructor()
	if value == nil {
		return reflect.Value{}, fmt.Errorf("constructor for %q returned nil", name)
	}
	pointer := reflect.ValueOf(value)
	if pointer.Kind() != reflect.Ptr {
		return reflect.Value{}, fmt.Errorf("constructor for %q is not a pointer", name)
	}
	if err := fillTDObject(pointer, object); err != nil {
		return reflect.Value{}, err
	}
	return pointer, nil
}

func tdToGo(value reflect.Value) any {
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		return tdToGo(value.Elem())
	}
	if value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return nil
		}
		if value.CanInterface() {
			if object, ok := value.Interface().(tdp.Object); ok {
				return tdObjectToGo(value.Elem(), object.TypeInfo())
			}
		}
		return tdToGo(value.Elem())
	}
	if value.Kind() == reflect.Struct {
		copy := reflect.New(value.Type()).Elem()
		copy.Set(value)
		if object, ok := copy.Addr().Interface().(tdp.Object); ok {
			return tdObjectToGo(copy, object.TypeInfo())
		}
	}
	switch value.Kind() {
	case reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return value.Bytes()
		}
		items := make([]any, value.Len())
		for i := range items {
			items[i] = tdToGo(value.Index(i))
		}
		return items
	case reflect.Array:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			bytes := make([]byte, value.Len())
			reflect.Copy(reflect.ValueOf(bytes), value)
			return bytes
		}
		items := make([]any, value.Len())
		for i := range items {
			items[i] = tdToGo(value.Index(i))
		}
		return items
	case reflect.Map:
		object := make(map[string]any, value.Len())
		iter := value.MapRange()
		for iter.Next() {
			object[fmt.Sprint(iter.Key().Interface())] = tdToGo(iter.Value())
		}
		return object
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		number := value.Int()
		if number > 1<<53 || number < -(1<<53) {
			return big.NewInt(number)
		}
		return number
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		number := value.Uint()
		if number > 1<<53 {
			return new(big.Int).SetUint64(number)
		}
		return number
	case reflect.Float32, reflect.Float64:
		return value.Float()
	case reflect.Bool:
		return value.Bool()
	case reflect.String:
		return value.String()
	default:
		if value.CanInterface() {
			return value.Interface()
		}
		return nil
	}
}

func tdObjectToGo(value reflect.Value, info tdp.Type) any {
	object := make(map[string]any, len(info.Fields)+1)
	object["@type"] = info.Name
	for _, field := range info.Fields {
		if field.Null {
			continue
		}
		target := value.FieldByName(field.Name)
		if !target.IsValid() {
			continue
		}
		object[field.SchemaName] = tdToGo(target)
	}
	return object
}

func jsInt64(r *moejs.Realm, v moejs.Value) (int64, error) {
	f, err := r.ToNumber(v)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f || math.Abs(f) > jsMaxSafeInteger {
		return 0, fmt.Errorf("expected a safe integer, got %v", f)
	}
	return int64(f), nil
}

func jsInt(r *moejs.Realm, v moejs.Value) (int, error) {
	n, err := jsInt64(r, v)
	if err != nil {
		return 0, err
	}
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("integer out of range: %d", n)
	}
	return int(n), nil
}

func jsString(r *moejs.Realm, v moejs.Value) (string, error) {
	s, err := r.ToString(v)
	if err != nil {
		return "", err
	}
	return s.GoString(), nil
}

func jsIntSlice(r *moejs.Realm, v moejs.Value) ([]int, error) {
	switch values := r.ToGo(v).(type) {
	case []any:
		ids := make([]int, 0, len(values))
		for _, value := range values {
			id, err := jsAnyToInt(value)
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, nil
	case []int:
		return values, nil
	case []int64:
		ids := make([]int, 0, len(values))
		for _, value := range values {
			id, err := jsFloatToInt(float64(value))
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, nil
	case []float64:
		ids := make([]int, 0, len(values))
		for _, value := range values {
			id, err := jsFloatToInt(value)
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, nil
	default:
		return nil, fmt.Errorf("expected an array of integers")
	}
}

func jsAnyToInt(value any) (int, error) {
	switch n := value.(type) {
	case int:
		return jsRangeInt(int64(n))
	case int64:
		return jsRangeInt(n)
	case float64:
		return jsFloatToInt(n)
	default:
		return 0, fmt.Errorf("expected an array of integers")
	}
}

func jsRangeInt(n int64) (int, error) {
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("integer out of range: %d", n)
	}
	return int(n), nil
}

func jsFloatToInt(f float64) (int, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f || math.Abs(f) > jsMaxSafeInteger {
		return 0, fmt.Errorf("expected a safe integer, got %v", f)
	}
	if f < math.MinInt32 || f > math.MaxInt32 {
		return 0, fmt.Errorf("integer out of range: %v", f)
	}
	return int(f), nil
}
