package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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
)

var storedKeyHash = make([]byte, sha256.Size)
var validate = validator.New()

func validateApiKey(ctx *fiber.Ctx, key string) (bool, error) {
	if config.C.Api.Key == "" || storedKeyHash == nil {
		return true, nil // No API key required
	}
	if key == "" {
		return false, keyauth.ErrMissingOrMalformedAPIKey
	}
	inputsum := sha256.Sum256([]byte(key))
	inputHash := inputsum[:]
	if subtle.ConstantTimeCompare(inputHash, storedKeyHash) != 1 {
		return false, keyauth.ErrMissingOrMalformedAPIKey
	}
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
		copy(storedKeyHash, sum[:])
	}
	rg.Get("/indexed", GetIndexed)
	rg.Get("/index/:chat_id<int>", GetIndexInfo)
	rg.Post("/index/multi-search", SearchOnMultiChatByPost)
	rg.Get("/index/:chat_id<int>/search", SearchOnChatByGet)
	rg.Post("/index/:chat_id<int>/search", SearchOnChatByPost)
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
