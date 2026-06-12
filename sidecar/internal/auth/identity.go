package auth

import "context"

// Identity is the authenticated principal behind a request. In local
// (loopback) mode it is always LocalIdentity; in remote mode it is derived
// from the verified device token's claims. P1.3 threads this through the store
// layer so data access is scoped per identity (single-user remains the default).
type Identity struct {
	UserID    string
	AccountID string
	DeviceID  string
}

// LocalIdentity is the single principal used in local mode — the degenerate
// "one user" case the whole system defaults to.
var LocalIdentity = Identity{UserID: "local", AccountID: "local", DeviceID: "local"}

type identityCtxKey struct{}

// WithIdentity returns a copy of ctx carrying id.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// IdentityFromContext returns the request's identity, defaulting to
// LocalIdentity when none was attached (defensive: keeps single-user code paths
// working even if a handler is reached without the auth middleware).
func IdentityFromContext(ctx context.Context) Identity {
	if id, ok := ctx.Value(identityCtxKey{}).(Identity); ok {
		return id
	}
	return LocalIdentity
}
