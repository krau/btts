package userclient

import (
	"context"
	"errors"

	"github.com/krau/btts/userclient/extension"
)

func (u *UserClient) CallExtenApi(ctx context.Context, name string, input map[string]any) (map[string]any, error) {
	if u.ectx == nil {
		u.ectx = u.TClient.CreateContext()
	}
	if fn, ok := extension.GetExtenApiFunc(name); ok {
		return fn(ctx, u.ectx, input)
	}
	return nil, errors.New("unknown extension API: " + name)
}
