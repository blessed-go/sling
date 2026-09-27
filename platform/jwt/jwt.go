// Package jwt provides JWT signing, verification, and HTTP authentication middleware.
package jwt

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"uuid"

	"github.com/blessed-go/sling/platform/httpx"
	"github.com/blessed-go/sling/platform/logger"
	"github.com/golang-jwt/jwt/v5"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

var (
	// ErrTokenExpired indicates that the provided JWT has expired.
	ErrTokenExpired = errors.New("token is expired")
	// ErrInvalidToken indicates that the provided JWT is malformed or has an invalid signature.
	ErrInvalidToken = errors.New("invalid token")
	// ErrMissingToken indicates that the Authorization header is absent.
	ErrMissingToken = errors.New("missing authorization header")
)

// Config defines JWT authentication parameters.
type Config struct {
	Secret     string        `toml:"secret" env:"JWT_SECRET" env-required:"true" comment:"JWT HMAC-SHA256 secret key"`
	Expiration time.Duration `toml:"expiration" env:"JWT_EXPIRATION" env-default:"24h" comment:"Access token expiration duration"`
}

// TokenSigner generates signed access tokens for users.
type TokenSigner interface {
	SignAccess(userID uuid.UUID) (string, error)
}

// TokenVerifier validates and extracts user identity from access tokens.
type TokenVerifier interface {
	VerifyToken(ctx context.Context, tokenStr string) (uuid.UUID, error)
}

// UserClaims defines custom JWT claims including user ID.
type UserClaims struct {
	jwt.RegisteredClaims
	UserID string `json:"user_id"`
}

// HS256 implements TokenSigner and TokenVerifier using HMAC-SHA256.
type HS256 struct {
	secretKey []byte
	duration  time.Duration
}

// NewHS256 creates a new HMAC-SHA256 signer and verifier.
func NewHS256(cfg Config) *HS256 {
	return &HS256{
		secretKey: []byte(cfg.Secret),
		duration:  cfg.Expiration,
	}
}

// SignAccess generates a signed access token for the given user ID.
func (s *HS256) SignAccess(userID uuid.UUID) (string, error) {
	now := time.Now()
	claims := UserClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(s.duration)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
		UserID: userID.String(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secretKey)
}

// VerifyToken parses and verifies the token string, returning the associated user ID.
func (s *HS256) VerifyToken(ctx context.Context, tokenStr string) (uuid.UUID, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &UserClaims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return s.secretKey, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return uuid.Nil(), ErrTokenExpired
		}
		return uuid.Nil(), ErrInvalidToken
	}

	claims, ok := token.Claims.(*UserClaims)
	if !ok || !token.Valid {
		return uuid.Nil(), ErrInvalidToken
	}

	parsedID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return uuid.Nil(), ErrInvalidToken
	}

	return parsedID, nil
}

type userCtxKey struct{}

// WithUserID stores the user ID in the context.
func WithUserID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, userCtxKey{}, id)
}

// UserID extracts the user ID from the context if present.
func UserID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userCtxKey{}).(uuid.UUID)
	return id, ok
}

// MustUserID extracts the user ID from the context or panics if not found.
func MustUserID(ctx context.Context) uuid.UUID {
	id, ok := UserID(ctx)
	if !ok {
		panic("jwt: user_id not found in context (missing RequireAuth middleware?)")
	}
	return id
}

func enrichContext(ctx context.Context, userID uuid.UUID) context.Context {
	ctx = WithUserID(ctx, userID)

	log := logger.FromContext(ctx).With(slog.String("user_id", userID.String()))
	ctx = logger.WithContext(ctx, log)

	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.SetAttributes(semconv.EnduserIDKey.String(userID.String()))
	}

	return ctx
}

func extractBearerToken(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", ErrMissingToken
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", ErrInvalidToken
	}
	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", ErrInvalidToken
	}
	return token, nil
}

// RequireAuth enforces valid JWT authentication. If the token is missing, expired,
// or invalid, it responds with 401 Unauthorized via httpx.WriteError.
func RequireAuth(verifier TokenVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr, err := extractBearerToken(r)
			if err != nil {
				httpx.WriteError(w, r, httpx.Error{Status: http.StatusUnauthorized, Msg: err.Error()})
				return
			}

			userID, err := verifier.VerifyToken(r.Context(), tokenStr)
			if err != nil {
				httpx.WriteError(w, r, httpx.Error{Status: http.StatusUnauthorized, Msg: err.Error()})
				return
			}

			ctx := enrichContext(r.Context(), userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// OptionalAuth inspects the Authorization header if present. Valid tokens enrich the context,
// missing headers continue unauthenticated, and invalid tokens return 401 Unauthorized.
func OptionalAuth(verifier TokenVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr, err := extractBearerToken(r)
			if err != nil {
				if errors.Is(err, ErrMissingToken) {
					next.ServeHTTP(w, r)
					return
				}
				httpx.WriteError(w, r, httpx.Error{Status: http.StatusUnauthorized, Msg: err.Error()})
				return
			}

			userID, err := verifier.VerifyToken(r.Context(), tokenStr)
			if err != nil {
				httpx.WriteError(w, r, httpx.Error{Status: http.StatusUnauthorized, Msg: err.Error()})
				return
			}

			ctx := enrichContext(r.Context(), userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
