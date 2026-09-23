package auth

import (
	"context"
	"github.com/jackc/pgx/v5"
)

type sessionContextKey struct{}

func WithSession(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, token)
}

// RevalidateMutation locks the current user for the final artifact-binding
// transaction. Production upload adapters always attach the browser session.
func RevalidateMutation(ctx context.Context, tx pgx.Tx, userID int64, admin bool) error {
	var role, state string
	if err := tx.QueryRow(ctx, `SELECT role,status FROM users WHERE id=$1 FOR SHARE`, userID).Scan(&role, &state); err != nil {
		return err
	}
	if state != "active" || admin && role != RoleAdmin {
		return ErrNotPermitted
	}
	if token, ok := ctx.Value(sessionContextKey{}).(string); ok {
		hash, valid := sessionTokenHash(token)
		var found int64
		if !valid {
			return ErrSessionInvalid
		}
		if err := tx.QueryRow(ctx, `SELECT id FROM sessions WHERE token_hash=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at>now() FOR SHARE`, hash, userID).Scan(&found); err != nil {
			return ErrSessionInvalid
		}
	}
	return nil
}
