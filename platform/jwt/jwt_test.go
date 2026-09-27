package jwt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"uuid"

	"github.com/blessed-go/sling/platform/jwt"
)

func TestJWT_SignAndVerify(t *testing.T) {
	cfg := jwt.Config{
		Secret:     "super-secret-key-12345",
		Expiration: 1 * time.Hour,
	}
	signer := jwt.NewHS256(cfg)
	userID := uuid.NewV7()

	tokenStr, err := signer.SignAccess(userID)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	gotID, err := signer.VerifyToken(context.Background(), tokenStr)
	if err != nil {
		t.Fatalf("failed to verify token: %v", err)
	}
	if gotID != userID {
		t.Fatalf("expected userID %s, got %s", userID, gotID)
	}
}

func TestJWT_ExpiredToken(t *testing.T) {
	cfg := jwt.Config{
		Secret:     "super-secret-key-12345",
		Expiration: -1 * time.Minute, // expired token
	}
	signer := jwt.NewHS256(cfg)
	userID := uuid.NewV7()

	tokenStr, _ := signer.SignAccess(userID)

	_, err := signer.VerifyToken(context.Background(), tokenStr)
	if err != jwt.ErrTokenExpired {
		t.Fatalf("expected ErrTokenExpired, got %v", err)
	}
}

func TestJWT_RequireAuthMiddleware(t *testing.T) {
	cfg := jwt.Config{Secret: "my-test-secret", Expiration: time.Hour}
	signer := jwt.NewHS256(cfg)
	userID := uuid.NewV7()
	token, _ := signer.SignAccess(userID)

	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		ctxID := jwt.MustUserID(r.Context())
		if ctxID != userID {
			t.Fatalf("expected context user_id %s, got %s", userID, ctxID)
		}
		w.WriteHeader(http.StatusOK)
	})

	mw := jwt.RequireAuth(signer)(testHandler)

	// Missing token returns 401.
	req := httptest.NewRequest("GET", "/protected", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing token, got %d", rec.Code)
	}

	// Valid token returns 200.
	req = httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid token, got %d", rec.Code)
	}
	if !handlerCalled {
		t.Fatal("expected handler to be called")
	}
}

func TestJWT_OptionalAuthMiddleware(t *testing.T) {
	cfg := jwt.Config{Secret: "my-test-secret", Expiration: time.Hour}
	signer := jwt.NewHS256(cfg)
	userID := uuid.NewV7()
	token, _ := signer.SignAccess(userID)

	mw := jwt.OptionalAuth(signer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := jwt.UserID(r.Context())
		if ok {
			w.Header().Set("X-User-ID", id.String())
		}
		w.WriteHeader(http.StatusOK)
	}))

	// Anonymous request returns 200 without user ID.
	req := httptest.NewRequest("GET", "/public", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("X-User-ID") != "" {
		t.Fatal("expected anonymous request to have no user_id")
	}

	// Authenticated request populates user ID.
	req = httptest.NewRequest("GET", "/public", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("X-User-ID") != userID.String() {
		t.Fatalf("expected user_id %s, got %s", userID, rec.Header().Get("X-User-ID"))
	}
}
