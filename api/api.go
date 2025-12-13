package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/swagger"
	_ "github.com/krau/btts/api/docs"
	"github.com/krau/btts/webembed"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/gofiber/fiber/v2/middleware/keyauth"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/krau/btts/config"
	"github.com/krau/btts/database"
	"gorm.io/gorm"
)

var storedKeyHash []byte
var validate = validator.New()

func validateApiKey(ctx *fiber.Ctx, key string) (bool, error) {
	// 未配置主 API key 时，保持原有行为：不要求鉴权
	if config.C.Api.Key == "" {
		return true, nil
	}
	if key == "" {
		return false, keyauth.ErrMissingOrMalformedAPIKey
	}
	inputsum := sha256.Sum256([]byte(key))
	inputHash := inputsum[:]
	// 先校验是否为超级管理 key
	if storedKeyHash != nil && subtle.ConstantTimeCompare(inputHash, storedKeyHash) == 1 {
		ctx.Locals("api_master", true)
		return true, nil
	}
	// 再尝试匹配子 API key（按哈希查询）
	hexHash := hex.EncodeToString(inputHash)
	apiKey, err := database.GetApiKeyByHash(ctx.Context(), hexHash)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, keyauth.ErrMissingOrMalformedAPIKey
		}
		return false, err
	}
	ctx.Locals("api_master", false)
	ctx.Locals("api_key_id", apiKey.ID)
	ctx.Locals("api_key_chats", apiKey.ChatIDs)
	return true, nil
}

// @title						BTTS API
// @version					1.0
// @description				Better Telegram Search API
// @BasePath					/api
// @securityDefinitions.apikey	ApiKeyAuth
// @in							header
// @name						Authorization
// @description				Type "Bearer" followed by a space and JWT token.
func Serve(addr string) {
	app := fiber.New(
		fiber.Config{
			JSONEncoder: sonic.Marshal,
			JSONDecoder: sonic.Unmarshal,
		},
	)
	loggerCfg := logger.ConfigDefault
	loggerCfg.Format = "${time} | ${status} | ${latency} | ${ip} | ${method} | ${path} | ${queryParams} | ${error}\n"
	app.Use(logger.New(loggerCfg))
	app.Use(cors.New())

	app.Get("/docs/*", swagger.HandlerDefault)
	rg := app.Group("/api")
	if config.C.Api.Key != "" {
		rg.Use(keyauth.New(keyauth.Config{
			Validator: validateApiKey,
			Next: func(c *fiber.Ctx) bool {
				return c.Path() == "/api/client/filestream"
			},
		}))
		sum := sha256.Sum256([]byte(config.C.Api.Key))
		storedKeyHash = sum[:]
	}
	rg.Get("/indexed", GetIndexed)
	rg.Get("/index/:chat_id<int>", GetIndexInfo)
	rg.Post("/index/multi-search", SearchOnMultiChatByPost)
	rg.Get("/index/:chat_id<int>/search", SearchOnChatByGet)
	rg.Post("/index/:chat_id<int>/search", SearchOnChatByPost)
	rg.Post("/index/:chat_id<int>/msgs/fetch", FetchMessages)
	rg.Post("/client/reply", ReplyMessage)
	rg.Post("/client/forward", ForwardMessages)
	rg.Use("/client/filestream", keyauth.New(keyauth.Config{
		Validator: func(c *fiber.Ctx, s string) (bool, error) {
			if config.C.Api.Key == "" {
				return true, nil
			}
			if s == "" {
				return false, keyauth.ErrMissingOrMalformedAPIKey
			}
			if c.Query("chat_id", "") == "" || c.Query("message_id", "") == "" {
				return false, keyauth.ErrMissingOrMalformedAPIKey
			}
			validKeyStr := fmt.Sprintf("%s:%s:%s", config.C.Api.Key, c.Query("chat_id", "Hatsune"), c.Query("message_id", "Miku"))
			validKeyHash := sha256.Sum256([]byte(validKeyStr))
			hexValidKey := hex.EncodeToString(validKeyHash[:])
			if len(s) != len(hexValidKey) {
				return false, keyauth.ErrMissingOrMalformedAPIKey
			}
			if subtle.ConstantTimeCompare([]byte(s), []byte(hexValidKey)) != 1 {
				return false, keyauth.ErrMissingOrMalformedAPIKey
			}
			return true, nil
		},
		KeyLookup: "query:reqtoken",
	}))
	rg.Get("/client/filestream", StreamFile)
	rg.Post("/client/callexten/:exten<string>", CallClientExtension)

	app.Use("/", filesystem.New(filesystem.Config{
		Root:         http.FS(webembed.Static),
		NotFoundFile: "404.html",
	}))

	go func() {
		if err := app.Listen(addr); err != nil {
			panic(err)
		}
	}()
}
