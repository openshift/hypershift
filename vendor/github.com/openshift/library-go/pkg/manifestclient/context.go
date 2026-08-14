package manifestclient

import (
	"context"
)

type ctxKey struct{}

var controllerNameCtxKey = ctxKey{}

func WithControllerInstanceNameFromContext(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, controllerNameCtxKey, name)
}

func ControllerInstanceNameFromContext(ctx context.Context) string {
	val, _ := ctx.Value(controllerNameCtxKey).(string)
	return val
}
