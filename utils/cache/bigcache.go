package cache

import (
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/dgraph-io/ristretto/v2"
)

var cache *ristretto.Cache[string, any]

func init() {
	c, err := ristretto.NewCache(&ristretto.Config[string, any]{
		NumCounters: 1e7, // 10M keys
		MaxCost:     1e8, // 100M values
		BufferItems: 64,
	})
	if err != nil {
		log.Fatalf("Failed to create cache: %v", err)
	}
	cache = c
}

func Set(key string, value any) error {
	ok := cache.Set(key, value, 1)
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
