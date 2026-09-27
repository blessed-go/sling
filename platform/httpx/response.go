package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/blessed-go/sling/platform/ctxerr"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type errorMapKey struct{}

// JSON writes a serialized JSON response with the given HTTP status code.
func JSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// MapErrors registers an error-to-status mapping into the request context.
func MapErrors(mapping map[error]int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), errorMapKey{}, mapping)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func matchError(err error, mapping map[error]int) (int, string, bool) {
	for curr := err; curr != nil; curr = unwrapSingle(curr) {
		if status, ok := mapping[curr]; ok {
			return status, curr.Error(), true
		}

		if matcher, ok := curr.(interface{ Is(error) bool }); ok {
			for targetErr, status := range mapping {
				if matcher.Is(targetErr) {
					return status, targetErr.Error(), true
				}
			}
		}
	}

	return 0, "", false
}

// unwrapSingle unwraps nested errors, supporting Go 1.20+ multi-errors (errors.Join).
func unwrapSingle(err error) error {
	u, ok := err.(interface{ Unwrap() error })
	if ok {
		return u.Unwrap()
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		errs := multi.Unwrap()
		if len(errs) > 0 {
			return errs[0]
		}
	}
	return nil
}

// WriteError inspects the error chain, resolves matching HTTP status codes,
// updates OpenTelemetry span status on 5xx errors, and writes a standardized JSON error response.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	msg := "internal error"

	if mapping, ok := r.Context().Value(errorMapKey{}).(map[error]int); ok {
		if mappedStatus, mappedMsg, found := matchError(err, mapping); found {
			status = mappedStatus
			msg = mappedMsg
		}
	}

	var he Error
	if errors.As(err, &he) {
		status = he.Status
		msg = he.Msg
	}

	span := trace.SpanFromContext(r.Context())
	if status >= 500 {
		span.SetStatus(codes.Error, msg)
		span.RecordError(err)
	}

	ctxerr.SetErr(r.Context(), err)

	JSON(w, status, map[string]string{"error": msg})
}

// BadRequest writes a 400 Bad Request error response.
func BadRequest(w http.ResponseWriter, r *http.Request, msg string) {
	WriteError(w, r, Error{Status: http.StatusBadRequest, Msg: msg})
}

// NotFound writes a 404 Not Found error response.
func NotFound(w http.ResponseWriter, r *http.Request, msg string) {
	WriteError(w, r, Error{Status: http.StatusNotFound, Msg: msg})
}
