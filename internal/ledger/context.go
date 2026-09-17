package ledger

import (
	"context"

	"github.com/google/uuid"
)

type ctxKey string

const (
	ctxCallerIDKey   ctxKey = "caller_id"
	ctxCallerRoleKey ctxKey = "caller_role"
	ctxKeyIDKey      ctxKey = "key_id"
	ctxKeyPrefixKey  ctxKey = "key_prefix"
)

func WithCaller(ctx context.Context, userID uuid.UUID, role string, keyID uuid.UUID, prefix string) context.Context {
	ctx = context.WithValue(ctx, ctxCallerIDKey, userID)
	ctx = context.WithValue(ctx, ctxCallerRoleKey, role)
	ctx = context.WithValue(ctx, ctxKeyIDKey, keyID)
	ctx = context.WithValue(ctx, ctxKeyPrefixKey, prefix)
	return ctx
}

func CallerFromContext(ctx context.Context) (userID uuid.UUID, role string, ok bool) {
	uid, ok1 := ctx.Value(ctxCallerIDKey).(uuid.UUID)
	r, ok2 := ctx.Value(ctxCallerRoleKey).(string)
	return uid, r, ok1 && ok2
}

func IsService(ctx context.Context) bool {
	_, role, ok := CallerFromContext(ctx)
	return ok && role == "service"
}
