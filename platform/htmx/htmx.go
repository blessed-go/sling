package htmx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// HTMX request header constants.
const (
	HeaderRequest               = "HX-Request"
	HeaderBoosted               = "HX-Boosted"
	HeaderCurrentURL            = "HX-Current-URL"
	HeaderHistoryRestoreRequest = "HX-History-Restore-Request"
	HeaderPrompt                = "HX-Prompt"
	HeaderTarget                = "HX-Target"
	HeaderTrigger               = "HX-Trigger"
	HeaderTriggerName           = "HX-Trigger-Name"
)

// HTMX response header constants.
const (
	HeaderLocation                   = "HX-Location"
	HeaderPushURL                    = "HX-Push-Url"
	HeaderReplaceURL                 = "HX-Replace-Url"
	HeaderRedirect                   = "HX-Redirect"
	HeaderRefresh                    = "HX-Refresh"
	HeaderReswap                     = "HX-Reswap"
	HeaderRetarget                   = "HX-Retarget"
	HeaderReselect                   = "HX-Reselect"
	HeaderResponseTrigger            = "HX-Trigger"
	HeaderResponseTriggerAfterSwap   = "HX-Trigger-After-Swap"
	HeaderResponseTriggerAfterSettle = "HX-Trigger-After-Settle"
)

// SwapStrategy defines HTMX swap behaviors for HX-Reswap.
type SwapStrategy string

const (
	SwapInnerHTML   SwapStrategy = "innerHTML"
	SwapOuterHTML   SwapStrategy = "outerHTML"
	SwapBeforeBegin SwapStrategy = "beforebegin"
	SwapAfterBegin  SwapStrategy = "afterbegin"
	SwapBeforeEnd   SwapStrategy = "beforeend"
	SwapAfterEnd    SwapStrategy = "afterend"
	SwapDelete      SwapStrategy = "delete"
	SwapNone        SwapStrategy = "none"
)

// IsHTMX reports whether the incoming request was issued by HTMX.
func IsHTMX(r *http.Request) bool {
	return r.Header.Get(HeaderRequest) == "true"
}

// IsBoosted reports whether the request was boosted via hx-boost.
func IsBoosted(r *http.Request) bool {
	return r.Header.Get(HeaderBoosted) == "true"
}

// IsHistoryRestore reports whether the request is for history restoration after a cache miss.
func IsHistoryRestore(r *http.Request) bool {
	return r.Header.Get(HeaderHistoryRestoreRequest) == "true"
}

// CurrentURL returns the browser's current URL when the request was made.
func CurrentURL(r *http.Request) string {
	return r.Header.Get(HeaderCurrentURL)
}

// Prompt returns the user response entered into an hx-prompt dialog.
func Prompt(r *http.Request) string {
	return r.Header.Get(HeaderPrompt)
}

// Target returns the ID of the target element (HX-Target header).
func Target(r *http.Request) string {
	return r.Header.Get(HeaderTarget)
}

// TriggerID returns the ID of the element that triggered the request (HX-Trigger header).
func TriggerID(r *http.Request) string {
	return r.Header.Get(HeaderTrigger)
}

// TriggerName returns the name of the element that triggered the request (HX-Trigger-Name header).
func TriggerName(r *http.Request) string {
	return r.Header.Get(HeaderTriggerName)
}

// Redirect instructs HTMX to perform a client-side redirect to the given URL (HX-Redirect).
func Redirect(w http.ResponseWriter, url string) {
	w.Header().Set(HeaderRedirect, url)
}

// Refresh instructs HTMX to perform a full page refresh (HX-Refresh: "true").
func Refresh(w http.ResponseWriter) {
	w.Header().Set(HeaderRefresh, "true")
}

// Location instructs HTMX to perform a client-side navigation without full page reload (HX-Location).
func Location(w http.ResponseWriter, pathOrJSON string) {
	w.Header().Set(HeaderLocation, pathOrJSON)
}

// PushURL pushes a new URL into the browser history stack (HX-Push-Url).
func PushURL(w http.ResponseWriter, url string) {
	w.Header().Set(HeaderPushURL, url)
}

// PreventPushURL prevents updating the browser history URL (HX-Push-Url: "false").
func PreventPushURL(w http.ResponseWriter) {
	w.Header().Set(HeaderPushURL, "false")
}

// ReplaceURL replaces the current URL in the browser location bar (HX-Replace-Url).
func ReplaceURL(w http.ResponseWriter, url string) {
	w.Header().Set(HeaderReplaceURL, url)
}

// PreventReplaceURL prevents replacing the browser location bar (HX-Replace-Url: "false").
func PreventReplaceURL(w http.ResponseWriter) {
	w.Header().Set(HeaderReplaceURL, "false")
}

// Reswap sets how the response will be swapped into the target element (HX-Reswap).
func Reswap(w http.ResponseWriter, strategy SwapStrategy) {
	w.Header().Set(HeaderReswap, string(strategy))
}

// Retarget overrides the target of the swap with a CSS selector (HX-Retarget).
func Retarget(w http.ResponseWriter, selector string) {
	w.Header().Set(HeaderRetarget, selector)
}

// Reselect selects a subset of the response HTML using a CSS selector (HX-Reselect).
func Reselect(w http.ResponseWriter, selector string) {
	w.Header().Set(HeaderReselect, selector)
}

// Trigger fires a client-side event immediately upon receiving the response (HX-Trigger).
// If multiple events are triggered on the same response, they are merged into a JSON map.
func Trigger(w http.ResponseWriter, event string) {
	addTrigger(w, HeaderResponseTrigger, event, nil)
}

// TriggerPayload fires a client-side event with a payload upon receiving the response (HX-Trigger).
func TriggerPayload(w http.ResponseWriter, event string, payload any) {
	addTrigger(w, HeaderResponseTrigger, event, payload)
}

// TriggerEvents fires multiple client-side events simultaneously via a JSON map (HX-Trigger).
func TriggerEvents(w http.ResponseWriter, events map[string]any) {
	addTriggerEvents(w, HeaderResponseTrigger, events)
}

// TriggerAfterSwap fires a client-side event after the swap step (HX-Trigger-After-Swap).
func TriggerAfterSwap(w http.ResponseWriter, event string) {
	addTrigger(w, HeaderResponseTriggerAfterSwap, event, nil)
}

// TriggerAfterSwapPayload fires a client-side event with a payload after the swap step.
func TriggerAfterSwapPayload(w http.ResponseWriter, event string, payload any) {
	addTrigger(w, HeaderResponseTriggerAfterSwap, event, payload)
}

// TriggerAfterSwapEvents fires multiple events after the swap step.
func TriggerAfterSwapEvents(w http.ResponseWriter, events map[string]any) {
	addTriggerEvents(w, HeaderResponseTriggerAfterSwap, events)
}

// TriggerAfterSettle fires a client-side event after the swap settles (HX-Trigger-After-Settle).
func TriggerAfterSettle(w http.ResponseWriter, event string) {
	addTrigger(w, HeaderResponseTriggerAfterSettle, event, nil)
}

// TriggerAfterSettlePayload fires a client-side event with a payload after the swap settles.
func TriggerAfterSettlePayload(w http.ResponseWriter, event string, payload any) {
	addTrigger(w, HeaderResponseTriggerAfterSettle, event, payload)
}

// TriggerAfterSettleEvents fires multiple events after the swap settles.
func TriggerAfterSettleEvents(w http.ResponseWriter, events map[string]any) {
	addTriggerEvents(w, HeaderResponseTriggerAfterSettle, events)
}

func addTrigger(w http.ResponseWriter, headerName string, event string, payload any) {
	current := w.Header().Get(headerName)
	if current == "" {
		if payload == nil {
			w.Header().Set(headerName, event)
			return
		}
		b, err := json.Marshal(map[string]any{event: payload})
		if err == nil {
			w.Header().Set(headerName, string(b))
		}
		return
	}

	// Existing header present: parse and merge into JSON map.
	merged := parseExistingTriggers(current)
	merged[event] = payload

	b, err := json.Marshal(merged)
	if err == nil {
		w.Header().Set(headerName, string(b))
	}
}

func addTriggerEvents(w http.ResponseWriter, headerName string, events map[string]any) {
	if len(events) == 0 {
		return
	}
	current := w.Header().Get(headerName)
	merged := parseExistingTriggers(current)
	for k, v := range events {
		merged[k] = v
	}

	b, err := json.Marshal(merged)
	if err == nil {
		w.Header().Set(headerName, string(b))
	}
}

func parseExistingTriggers(headerVal string) map[string]any {
	merged := make(map[string]any)
	trimmed := strings.TrimSpace(headerVal)
	if trimmed == "" {
		return merged
	}

	if strings.HasPrefix(trimmed, "{") {
		_ = json.Unmarshal([]byte(trimmed), &merged)
		return merged
	}

	// Comma-separated list of event names
	for _, part := range strings.Split(trimmed, ",") {
		name := strings.TrimSpace(part)
		if name != "" {
			merged[name] = nil
		}
	}
	return merged
}

var bufPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

// Template represents a template capable of executing a named template.
// Both standard *html/template.Template and *text/template.Template satisfy this interface.
type Template interface {
	ExecuteTemplate(wr io.Writer, name string, data any) error
}

type executer interface {
	Execute(wr io.Writer, data any) error
}

// Render executes a template into an in-memory buffer before writing to w.
// This guarantees that template execution errors return an error before any
// 200 OK headers or partial broken HTML are flushed to the client.
func Render(w http.ResponseWriter, status int, tmpl Template, name string, data any) error {
	if tmpl == nil {
		return errors.New("htmx: template is nil")
	}

	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufPool.Put(buf)

	var err error
	if name == "" {
		if ex, ok := tmpl.(executer); ok {
			err = ex.Execute(buf, data)
		} else {
			err = tmpl.ExecuteTemplate(buf, "", data)
		}
	} else {
		err = tmpl.ExecuteTemplate(buf, name, data)
	}

	if err != nil {
		return fmt.Errorf("htmx: render template %q: %w", name, err)
	}

	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	w.WriteHeader(status)
	_, err = buf.WriteTo(w)
	return err
}

// HTML writes a raw HTML string to the response writer with the given HTTP status code.
func HTML(w http.ResponseWriter, status int, html string) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, html)
}
