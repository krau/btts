package api

import (
	"strconv"
	"strings"

	"github.com/duke-git/lancet/v2/slice"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/keyauth"
	"github.com/krau/btts/config"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/types"
)

func validateApiKey(ctx *fiber.Ctx, key string) (bool, error) {
	if config.C.Api.Key == "" {
		return true, nil // No API key required
	}
	if key == "" {
		return false, keyauth.ErrMissingOrMalformedAPIKey
	}
	if key != config.C.Api.Key {
		return false, keyauth.ErrMissingOrMalformedAPIKey
	}
	return true, nil
}

func Serve(addr string) {
	app := fiber.New()
	app.Use(cors.New())
	if config.C.Api.Key != "" {
		app.Use(keyauth.New(keyauth.Config{
			Validator: validateApiKey,
		}))
	}
	app.Get("/indexed", func(c *fiber.Ctx) error {
		chats, err := database.GetAllIndexChats(c.Context())
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to retrieve indexed chats",
			})
		}
		if len(chats) == 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "No indexed chats found",
			})
		}
		return c.JSON(fiber.Map{
			"status": "success",
			"chats":  chats,
		})
	})
	app.Get("/index/:chat_id<int>/search", func(c *fiber.Ctx) error {
		chatID, err := c.ParamsInt("chat_id")
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Chat ID is required",
			})
		}
		query := c.Query("q")
		if query == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Query parameter 'q' is required",
			})
		}
		offset := c.QueryInt("offset")
		limit := c.QueryInt("limit", 10)

		req := types.SearchRequest{
			ChatID: int64(chatID),
			Query:  query,
			Offset: int64(offset),
			Limit:  int64(limit),
		}
		if users := c.Query("users"); users != "" {
			userIDs := slice.Compact(slice.Map(strings.Split(users, ","), func(i int, userId string) int64 {
				userID, err := strconv.ParseInt(userId, 10, 64)
				if err != nil {
					return 0
				}
				return userID
			}))
			if len(userIDs) > 0 {
				req.UserFilters = userIDs
			}
		}
		if msgTypeStr := c.Query("types"); msgTypeStr != "" {
			msgTypes := slice.Compact(slice.Map(strings.Split(msgTypeStr, ","), func(i int, msgType string) types.MessageType {
				return types.MessageTypeFromString[msgType]
			}))
			if len(msgTypes) > 0 {
				req.TypeFilters = msgTypes
			}
		}
		results, err := engine.Instance.Search(c.Context(), req)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to search index",
			})
		}
		return c.JSON(fiber.Map{
			"status":  "success",
			"results": results,
		})
	})

	go func() {
		if err := app.Listen(addr); err != nil {
			panic(err)
		}
	}()
}
