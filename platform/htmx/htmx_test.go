package htmx

import (
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestInspection(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	if IsHTMX(req) {
		t.Errorf("expected IsHTMX false on standard request")
	}
	if IsBoosted(req) {
		t.Errorf("expected IsBoosted false")
	}
	if IsHistoryRestore(req) {
		t.Errorf("expected IsHistoryRestore false")
	}
	if CurrentURL(req) != "" || Prompt(req) != "" || Target(req) != "" || TriggerID(req) != "" || TriggerName(req) != "" {
		t.Errorf("expected empty values for unconfigured headers")
	}

	req.Header.Set(HeaderRequest, "true")
	req.Header.Set(HeaderBoosted, "true")
	req.Header.Set(HeaderHistoryRestoreRequest, "true")
	req.Header.Set(HeaderCurrentURL, "http://localhost/items")
	req.Header.Set(HeaderPrompt, "User input")
	req.Header.Set(HeaderTarget, "content-div")
	req.Header.Set(HeaderTrigger, "submit-btn")
	req.Header.Set(HeaderTriggerName, "submit")

	if !IsHTMX(req) {
		t.Errorf("expected IsHTMX true")
	}
	if !IsBoosted(req) {
		t.Errorf("expected IsBoosted true")
	}
	if !IsHistoryRestore(req) {
		t.Errorf("expected IsHistoryRestore true")
	}
	if got := CurrentURL(req); got != "http://localhost/items" {
		t.Errorf("CurrentURL = %q, want %q", got, "http://localhost/items")
	}
	if got := Prompt(req); got != "User input" {
		t.Errorf("Prompt = %q, want %q", got, "User input")
	}
	if got := Target(req); got != "content-div" {
		t.Errorf("Target = %q, want %q", got, "content-div")
	}
	if got := TriggerID(req); got != "submit-btn" {
		t.Errorf("TriggerID = %q, want %q", got, "submit-btn")
	}
	if got := TriggerName(req); got != "submit" {
		t.Errorf("TriggerName = %q, want %q", got, "submit")
	}
}

func TestResponseNavigationAndSwap(t *testing.T) {
	rec := httptest.NewRecorder()
	Redirect(rec, "/dashboard")
	if got := rec.Header().Get(HeaderRedirect); got != "/dashboard" {
		t.Errorf("Redirect header = %q, want %q", got, "/dashboard")
	}

	rec = httptest.NewRecorder()
	Refresh(rec)
	if got := rec.Header().Get(HeaderRefresh); got != "true" {
		t.Errorf("Refresh header = %q, want %q", got, "true")
	}

	rec = httptest.NewRecorder()
	Location(rec, "/search")
	if got := rec.Header().Get(HeaderLocation); got != "/search" {
		t.Errorf("Location header = %q, want %q", got, "/search")
	}

	rec = httptest.NewRecorder()
	PushURL(rec, "/items/123")
	if got := rec.Header().Get(HeaderPushURL); got != "/items/123" {
		t.Errorf("PushURL = %q, want %q", got, "/items/123")
	}

	rec = httptest.NewRecorder()
	PreventPushURL(rec)
	if got := rec.Header().Get(HeaderPushURL); got != "false" {
		t.Errorf("PreventPushURL = %q, want %q", got, "false")
	}

	rec = httptest.NewRecorder()
	ReplaceURL(rec, "/items/current")
	if got := rec.Header().Get(HeaderReplaceURL); got != "/items/current" {
		t.Errorf("ReplaceURL = %q, want %q", got, "/items/current")
	}

	rec = httptest.NewRecorder()
	PreventReplaceURL(rec)
	if got := rec.Header().Get(HeaderReplaceURL); got != "false" {
		t.Errorf("PreventReplaceURL = %q, want %q", got, "false")
	}

	rec = httptest.NewRecorder()
	Reswap(rec, SwapOuterHTML)
	if got := rec.Header().Get(HeaderReswap); got != "outerHTML" {
		t.Errorf("Reswap = %q, want %q", got, "outerHTML")
	}

	rec = httptest.NewRecorder()
	Retarget(rec, "#modal-body")
	if got := rec.Header().Get(HeaderRetarget); got != "#modal-body" {
		t.Errorf("Retarget = %q, want %q", got, "#modal-body")
	}

	rec = httptest.NewRecorder()
	Reselect(rec, ".alert-box")
	if got := rec.Header().Get(HeaderReselect); got != ".alert-box" {
		t.Errorf("Reselect = %q, want %q", got, ".alert-box")
	}
}

func TestEventTriggers(t *testing.T) {
	t.Run("single string event", func(t *testing.T) {
		rec := httptest.NewRecorder()
		Trigger(rec, "userCreated")
		if got := rec.Header().Get(HeaderResponseTrigger); got != "userCreated" {
			t.Errorf("got %q, want %q", got, "userCreated")
		}
	})

	t.Run("single event with payload", func(t *testing.T) {
		rec := httptest.NewRecorder()
		TriggerPayload(rec, "showToast", map[string]string{"type": "success"})
		var m map[string]map[string]string
		if err := json.Unmarshal([]byte(rec.Header().Get(HeaderResponseTrigger)), &m); err != nil {
			t.Fatalf("failed to unmarshal trigger json: %v", err)
		}
		if m["showToast"]["type"] != "success" {
			t.Errorf("unexpected payload: %+v", m)
		}
	})

	t.Run("multiple triggers merge into JSON", func(t *testing.T) {
		rec := httptest.NewRecorder()
		Trigger(rec, "userCreated")
		Trigger(rec, "closeModal")
		TriggerPayload(rec, "notify", "Hello World")

		headerVal := rec.Header().Get(HeaderResponseTrigger)
		var m map[string]any
		if err := json.Unmarshal([]byte(headerVal), &m); err != nil {
			t.Fatalf("expected valid JSON from merged triggers, got %q (err: %v)", headerVal, err)
		}

		if _, ok := m["userCreated"]; !ok {
			t.Errorf("expected userCreated in JSON")
		}
		if _, ok := m["closeModal"]; !ok {
			t.Errorf("expected closeModal in JSON")
		}
		if m["notify"] != "Hello World" {
			t.Errorf("expected notify payload 'Hello World', got %v", m["notify"])
		}
	})

	t.Run("trigger events map", func(t *testing.T) {
		rec := httptest.NewRecorder()
		TriggerEvents(rec, map[string]any{
			"eventA": 1,
			"eventB": true,
		})

		var m map[string]any
		if err := json.Unmarshal([]byte(rec.Header().Get(HeaderResponseTrigger)), &m); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if m["eventA"] != float64(1) || m["eventB"] != true {
			t.Errorf("unexpected map content: %+v", m)
		}
	})

	t.Run("after swap and after settle triggers", func(t *testing.T) {
		rec := httptest.NewRecorder()
		TriggerAfterSwap(rec, "swapDone")
		TriggerAfterSwapPayload(rec, "swapMetric", 42)
		if got := rec.Header().Get(HeaderResponseTriggerAfterSwap); got == "" {
			t.Errorf("expected TriggerAfterSwap header to be populated")
		}

		TriggerAfterSettle(rec, "settleDone")
		TriggerAfterSettlePayload(rec, "settleMetric", 100)
		if got := rec.Header().Get(HeaderResponseTriggerAfterSettle); got == "" {
			t.Errorf("expected TriggerAfterSettle header to be populated")
		}
	})
}

type errTemplate struct{}

func (errTemplate) ExecuteTemplate(w io.Writer, name string, data any) error {
	return errors.New("boom in template")
}

func TestRender(t *testing.T) {
	tmpl := template.Must(template.New("test").Parse(`{{define "user"}}<p>User: {{.Name}}</p>{{end}}`))

	t.Run("successful render", func(t *testing.T) {
		rec := httptest.NewRecorder()
		err := Render(rec, http.StatusOK, tmpl, "user", map[string]string{"Name": "Alice"})
		if err != nil {
			t.Fatalf("unexpected render error: %v", err)
		}

		if rec.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("expected text/html charset, got %q", ct)
		}
		if body := rec.Body.String(); body != "<p>User: Alice</p>" {
			t.Errorf("unexpected body: %q", body)
		}
	})

	t.Run("root template with empty name", func(t *testing.T) {
		rootTmpl := template.Must(template.New("").Parse(`<h1>Title: {{.}}</h1>`))
		rec := httptest.NewRecorder()
		err := Render(rec, http.StatusOK, rootTmpl, "", "Welcome")
		if err != nil {
			t.Fatalf("unexpected render error: %v", err)
		}
		if body := rec.Body.String(); body != "<h1>Title: Welcome</h1>" {
			t.Errorf("unexpected body: %q", body)
		}
	})

	t.Run("template error does not write premature headers", func(t *testing.T) {
		rec := httptest.NewRecorder()
		err := Render(rec, http.StatusOK, errTemplate{}, "broken", nil)
		if err == nil {
			t.Fatalf("expected error from broken template")
		}

		if rec.Header().Get("Content-Type") != "" {
			t.Errorf("expected no Content-Type set when template fails")
		}
		if rec.Body.Len() > 0 {
			t.Errorf("expected empty body when template fails, got %q", rec.Body.String())
		}
	})

	t.Run("nil template returns error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		if err := Render(rec, http.StatusOK, nil, "any", nil); err == nil {
			t.Errorf("expected error on nil template")
		}
	})
}

func TestHTML(t *testing.T) {
	rec := httptest.NewRecorder()
	HTML(rec, http.StatusCreated, "<div>Created</div>")

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status %d, got %d", http.StatusCreated, rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("expected text/html charset, got %q", ct)
	}
	if body := rec.Body.String(); body != "<div>Created</div>" {
		t.Errorf("unexpected body: %q", body)
	}
}
