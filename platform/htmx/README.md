# `platform/htmx`: HTMX Frontend Helpers

Package `htmx` provides idiomatic Go helpers for building modern, server-rendered web frontends with [HTMX](https://htmx.org/) on top of the Sling platform.

---

## 1. Design & Philosophy

* **Zero External Dependencies:** Implemented entirely using Go standard library (`net/http`, `encoding/json`, `sync`, `io`). Zero impact on dependency footprint.
* **Template Engine Agnostic:** Works out of the box with standard `html/template`, `text/template`, or any engine implementing `ExecuteTemplate`. (For `a-h/templ`, invoke its native `Component.Render(ctx, w)` directly or use `htmx.HTML`).
* **Strict Specification Compliance:** Explicitly separates request reading (`TriggerID`, `TriggerName`) from response event triggers (`Trigger`, `TriggerPayload`) to prevent naming collisions.
* **Automatic Event Merging:** Multiple trigger calls on the same response safely merge into a valid JSON object without clobbering earlier headers.
* **Buffer-Protected Rendering:** `htmx.Render` executes templates into an in-memory buffer pool before flushing to the client, preventing premature `200 OK` status codes or corrupted HTML fragments on template execution errors.

---

## 2. Request Inspection

HTMX transmits context through special request headers. Inspect them easily using type-safe helpers:

| Request Header | Helper Function | Description |
| :--- | :--- | :--- |
| `HX-Request` | `htmx.IsHTMX(r) bool` | `true` if the request was issued by HTMX |
| `HX-Boosted` | `htmx.IsBoosted(r) bool` | `true` if the request was triggered via `hx-boost` |
| `HX-History-Restore-Request` | `htmx.IsHistoryRestore(r) bool` | `true` if restoring page state from the local history cache |
| `HX-Current-URL` | `htmx.CurrentURL(r) string` | The current URL of the browser when the request was initiated |
| `HX-Prompt` | `htmx.Prompt(r) string` | User input entered in response to an `hx-prompt` dialog |
| `HX-Target` | `htmx.Target(r) string` | The ID of the target element intended to receive the swap |
| `HX-Trigger` | `htmx.TriggerID(r) string` | The ID of the element that triggered the request |
| `HX-Trigger-Name` | `htmx.TriggerName(r) string` | The `name` attribute of the element that triggered the request |

```go
func handleSearch(w http.ResponseWriter, r *http.Request) {
    if !htmx.IsHTMX(r) {
        // Direct browser navigation: render full page layout with header & footer
        renderFullPage(w, r)
        return
    }

    // HTMX partial request: render only table rows
    renderSearchResults(w, r)
}
```

---

## 3. Response Control: Navigation & Swapping

Control client-side navigation, history entries, and swap targets without custom JavaScript:

### Navigation & History
* `htmx.Redirect(w, "/login")` — Instructs the browser to perform a full client-side redirect (`HX-Redirect`).
* `htmx.Refresh(w)` — Instructs the browser to do a full page reload (`HX-Refresh: "true"`).
* `htmx.Location(w, "/dashboard")` — Navigates without a full reload (`HX-Location`).
* `htmx.PushURL(w, "/items?page=2")` — Pushes a new URL into the browser history stack (`HX-Push-Url`).
* `htmx.PreventPushURL(w)` — Explicitly prevents URL updates (`HX-Push-Url: "false"`).
* `htmx.ReplaceURL(w, "/items/42")` — Replaces current URL in location bar without push (`HX-Replace-Url`).
* `htmx.PreventReplaceURL(w)` — Explicitly prevents URL replacement (`HX-Replace-Url: "false"`).

### Swap Modifiers
* `htmx.Reswap(w, htmx.SwapOuterHTML)` — Overrides the swap strategy (`HX-Reswap`).
  Available constants: `SwapInnerHTML`, `SwapOuterHTML`, `SwapBeforeBegin`, `SwapAfterBegin`, `SwapBeforeEnd`, `SwapAfterEnd`, `SwapDelete`, `SwapNone`.
* `htmx.Retarget(w, "#modal-container")` — Overrides the target element using a CSS selector (`HX-Retarget`).
* `htmx.Reselect(w, ".card-body")` — Selects a subset of response HTML to swap (`HX-Reselect`).

---

## 4. Client-Side Event Triggers

HTMX allows servers to trigger custom events in the browser via headers. The `htmx` package supports single string events, event payloads, and multiple events simultaneously.

### Trigger Helper Functions

| Phase / Header | Single Event | Event with Payload | Bulk Map (`map[string]any`) |
| :--- | :--- | :--- | :--- |
| **Immediate** (`HX-Trigger`) | `htmx.Trigger(w, event)` | `htmx.TriggerPayload(w, event, payload)` | `htmx.TriggerEvents(w, events)` |
| **After Swap** (`HX-Trigger-After-Swap`) | `htmx.TriggerAfterSwap(w, event)` | `htmx.TriggerAfterSwapPayload(w, event, payload)` | `htmx.TriggerAfterSwapEvents(w, events)` |
| **After Settle** (`HX-Trigger-After-Settle`) | `htmx.TriggerAfterSettle(w, event)` | `htmx.TriggerAfterSettlePayload(w, event, payload)` | `htmx.TriggerAfterSettleEvents(w, events)` |

* **Immediate:** Fires as soon as the response is received by the client.
* **After Swap:** Fires after the DOM content has been swapped.
* **After Settle:** Fires after DOM settlement and CSS transitions finish.
### Automatic Event Merging
Calling multiple trigger helpers on the same `http.ResponseWriter` automatically aggregates them into a valid JSON map:

```go
func handleCreateItem(w http.ResponseWriter, r *http.Request) {
    // 1. Simple event trigger
    htmx.Trigger(w, "itemCreated")

    // 2. Event with payload
    htmx.TriggerPayload(w, "showToast", map[string]string{
        "type":    "success",
        "message": "Item saved successfully",
    })

    // Resolves automatically to:
    // HX-Trigger: {"itemCreated":null,"showToast":{"type":"success","message":"Item saved successfully"}}

    htmx.HTML(w, http.StatusOK, `<tr id="item-42"><td>Item 42</td></tr>`)
}
```

Alternatively, emit multiple events in a single call using `TriggerEvents`:
```go
htmx.TriggerEvents(w, map[string]any{
    "itemCreated": map[string]any{"id": 42, "name": "Widget"},
    "closeModal":  true,
    "refreshFeed": nil,
})
```

In the browser, listen to events directly or with Alpine.js:
```html
<body hx-on:item-created="console.log('Created!')"
      hx-on:show-toast="showNotification(event.detail)">
```

---

## 5. Rendering: Templates & HTML

### `htmx.Render(w, status, tmpl, name, data)`
Executes a named template from `*html/template.Template` or any implementation of `htmx.Template`.

```go
var tmpl = template.Must(template.ParseGlob("web/templates/*.html"))

func handleGetUsers(w http.ResponseWriter, r *http.Request) {
    users := []User{{Name: "Alice"}, {Name: "Bob"}}

    if htmx.IsHTMX(r) {
        // Return only the partial rows fragment
        _ = htmx.Render(w, http.StatusOK, tmpl, "user_rows.html", users)
        return
    }

    // Return the full page shell enclosing user_rows.html
    _ = htmx.Render(w, http.StatusOK, tmpl, "layout.html", users)
}
```

#### Why Buffered Execution Matters:
Standard Go `tmpl.Execute(w, data)` writes directly to the network socket. If execution encounters a template error halfway through, Go has already sent `200 OK` and a partial, broken HTML stream.  
`htmx.Render` renders into a pooled `bytes.Buffer` first. If execution fails, it aborts cleanly and returns the error without writing any corrupted bytes or premature headers to `w`.

### `htmx.HTML(w, status, html)`
For small HTML fragments, badges, or button state updates:

```go
htmx.HTML(w, http.StatusOK, `<span class="badge bg-success">Saved</span>`)
```

---

## 6. End-to-End Pattern: Full Page vs Partial Fragment

A canonical Sling pattern for building responsive HTMX applications with zero client framework overhead:

```
Browser Request
      │
      ▼
┌───────────────┐
│ htmx.IsHTMX?  │
└───┬───────┬───┘
    │       │
    │ Yes   │ No (Direct navigation / Bookmark / F5)
    ▼       ▼
┌────────┐ ┌────────────────┐
│ Render │ │  Render Full   │
│ Partial│ │ Page with Base │
│ (Rows) │ │  HTML Shell    │
└────────┘ └────────────────┘
```

### Handler Implementation
```go
package web

import (
	"html/template"
	"net/http"

	"github.com/blessed-go/sling/platform/htmx"
)

type Handler struct {
	tmpl *template.Template
}

func (h *Handler) ListItems(w http.ResponseWriter, r *http.Request) {
	items := fetchItems(r.Context())

	// If HTMX triggered this request, return only the rows fragment
	if htmx.IsHTMX(r) {
		if err := htmx.Render(w, http.StatusOK, h.tmpl, "items_list.html", items); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// Full page load: wrap with base shell containing <html>, <head>, HTMX script, and navigation
	data := map[string]any{
		"Title": "Inventory Management",
		"Items": items,
	}
	if err := htmx.Render(w, http.StatusOK, h.tmpl, "index.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
```
