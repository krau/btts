package cache

import (
	"fmt"
	"time"

	"github.com/charmbracelet/log"
	"github.com/dgraph-io/ristretto/v2"
)

var cache *ristretto.Cache[string, any]

func init() {
	c, err := ristretto.NewCache(&ristretto.Config[string, any]{
		NumCounters: 1e5,
		MaxCost:     1e6,
		BufferItems: 64,
		OnReject: func(item *ristretto.Item[any]) {
			log.Warnf("Cache item rejected: key=%d, value=%v", item.Key, item.Value)
		},
	})
	if err != nil {
		log.Fatalf("Failed to create cache: %v", err)
	}
	cache = c
}

func Set(key string, value any) error {
	ok := cache.SetWithTTL(key, value, 0, 86400*time.Second)
	if !ok {
		return fmt.Errorf("failed to set value in cache")
	}
	cache.Wait()
	return nil
}

func Get[T any](key string) (T, bool) {
	v, ok := cache.Get(key)
	if !ok {
		var zero T
		return zero, false
	}
	vT, ok := v.(T)
	if !ok {
		var zero T
		return zero, false
	}
	return vT, true
}
