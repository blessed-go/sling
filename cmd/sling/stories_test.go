package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/blessed-go/sling/platform/app"
	"github.com/blessed-go/sling/platform/ctxerr"
	"github.com/blessed-go/sling/platform/httpx"
	"github.com/blessed-go/sling/platform/logger"
	"github.com/blessed-go/sling/platform/telemetry"
	"github.com/go-chi/chi/v5"
)

// TestUserStory_GoldenFlow executes the complete Happy Path developer journey:
// Alice initializes a multi-service workspace, adds subdomains with isolated tables and caches,
// links the local platform, adds services while linked, unlinks, and builds all binaries cleanly.
func TestUserStory_GoldenFlow(t *testing.T) {
	tempDir := t.TempDir()
	projectName := "store"
	projectDir := filepath.Join(tempDir, projectName)

	slingRepoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("failed to resolve sling repo root: %v", err)
	}

	t.Log("Step 1: Alice verifies sling version")
	if version != "v0.1.0" {
		t.Fatalf("expected version v0.1.0, got %s", version)
	}

	t.Log("Step 2: Alice initializes project workspace with first service 'orders'")
	if err := initProject(projectDir, "orders"); err != nil {
		t.Fatalf("initProject failed: %v", err)
	}
	assertFileExists(t, filepath.Join(projectDir, "go.mod"))
	assertFileExists(t, filepath.Join(projectDir, "Taskfile.yml"))
	assertFileExists(t, filepath.Join(projectDir, "cmd", "orders", "main.go"))
	assertFileExists(t, filepath.Join(projectDir, "internal", "orders", "domain.go"))
	assertFileExists(t, filepath.Join(projectDir, "conf", "orders.toml"))

	t.Log("Step 3: Alice adds subdomain 'fulfillment' inside service 'orders'")
	if err := handleAdd(projectDir, "orders/fulfillment"); err != nil {
		t.Fatalf("handleAdd for orders/fulfillment failed: %v", err)
	}
	ordersFulfillmentDir := filepath.Join(projectDir, "internal", "orders", "fulfillment")
	assertFileExists(t, filepath.Join(ordersFulfillmentDir, "domain.go"))
	assertFileExists(t, filepath.Join(ordersFulfillmentDir, "repo_postgres.go"))
	assertFileExists(t, filepath.Join(ordersFulfillmentDir, "repo_cached.go"))
	assertFileExists(t, filepath.Join(ordersFulfillmentDir, "migrations", "20260101000000_init.sql"))

	// Verify isolated table and index names for orders/fulfillment
	sqlInit := readFile(t, filepath.Join(ordersFulfillmentDir, "migrations", "20260101000000_init.sql"))
	if !strings.Contains(sqlInit, "CREATE TABLE IF NOT EXISTS fulfillment_items") {
		t.Fatalf("orders/fulfillment SQL migration does not isolate table name:\n%s", sqlInit)
	}
	if !strings.Contains(sqlInit, "idx_fulfillment_items_created_at") {
		t.Fatalf("orders/fulfillment SQL migration does not isolate index name:\n%s", sqlInit)
	}

	// Verify scoped cache key prefix
	cachedRepo := readFile(t, filepath.Join(ordersFulfillmentDir, "repo_cached.go"))
	if !strings.Contains(cachedRepo, `"orders:fulfillment:items:%s"`) {
		t.Fatalf("orders/fulfillment cache key does not contain composite prefix:\n%s", cachedRepo)
	}

	t.Log("Step 4: Alice adds independent microservice 'billing'")
	if err := handleAdd(projectDir, "billing"); err != nil {
		t.Fatalf("handleAdd for billing failed: %v", err)
	}
	assertFileExists(t, filepath.Join(projectDir, "cmd", "billing", "main.go"))
	assertFileExists(t, filepath.Join(projectDir, "internal", "billing", "domain.go"))
	assertFileExists(t, filepath.Join(projectDir, "conf", "billing.toml"))

	t.Log("Step 5: Alice adds subdomain 'fulfillment' with the SAME name to service 'billing'")
	if err := handleAdd(projectDir, "billing/fulfillment"); err != nil {
		t.Fatalf("handleAdd for billing/fulfillment failed: %v", err)
	}
	billingFulfillmentDir := filepath.Join(projectDir, "internal", "billing", "fulfillment")
	assertFileExists(t, filepath.Join(billingFulfillmentDir, "domain.go"))

	// Verify billing/fulfillment table and cache isolation
	billingCachedRepo := readFile(t, filepath.Join(billingFulfillmentDir, "repo_cached.go"))
	if !strings.Contains(billingCachedRepo, `"billing:fulfillment:items:%s"`) {
		t.Fatalf("billing/fulfillment cache key prefix collision:\n%s", billingCachedRepo)
	}

	// Verify migration imports use distinct aliases in cmd/migrate/main.go
	migrateMain := readFile(t, filepath.Join(projectDir, "cmd", "migrate", "main.go"))
	if !strings.Contains(migrateMain, `orders_fulfillment "store/internal/orders/fulfillment"`) {
		t.Fatalf("cmd/migrate/main.go missing orders_fulfillment alias:\n%s", migrateMain)
	}
	if !strings.Contains(migrateMain, `billing_fulfillment "store/internal/billing/fulfillment"`) {
		t.Fatalf("cmd/migrate/main.go missing billing_fulfillment alias:\n%s", migrateMain)
	}
	if !strings.Contains(migrateMain, `"orders": {orders.Migration, orders_fulfillment.Migration}`) {
		t.Fatalf("cmd/migrate/main.go missing orders_fulfillment registration:\n%s", migrateMain)
	}
	if !strings.Contains(migrateMain, `"billing": {billing.Migration, billing_fulfillment.Migration}`) {
		t.Fatalf("cmd/migrate/main.go missing billing_fulfillment registration:\n%s", migrateMain)
	}

	t.Log("Step 6: Alice links local Sling platform")
	if err := linkProject(projectDir, slingRepoRoot); err != nil {
		t.Fatalf("linkProject failed: %v", err)
	}
	assertFileExists(t, filepath.Join(projectDir, "go.work"))
	assertFileExists(t, filepath.Join(projectDir, "deployment", "docker-compose.override.yaml"))
	assertFileExists(t, filepath.Join(projectDir, "deployment", "go.work.docker"))

	t.Log("Step 7: Alice adds third service 'notifications' while workspace is linked")
	if err := handleAdd(projectDir, "notifications"); err != nil {
		t.Fatalf("handleAdd for notifications failed: %v", err)
	}
	// Verify go.work preserved custom sling path and docker-compose.override.yaml includes notifications
	goWorkContent := readFile(t, filepath.Join(projectDir, "go.work"))
	if !strings.Contains(goWorkContent, "replace github.com/blessed-go/sling =>") {
		t.Fatalf("go.work lost replace directive after adding service while linked:\n%s", goWorkContent)
	}
	overrideContent := readFile(t, filepath.Join(projectDir, "deployment", "docker-compose.override.yaml"))
	if !strings.Contains(overrideContent, "notifications:") {
		t.Fatalf("docker-compose.override.yaml missing notifications service:\n%s", overrideContent)
	}

	t.Log("Step 8: Alice unlinks Sling platform")
	if err := unlinkProject(projectDir); err != nil {
		t.Fatalf("unlinkProject failed: %v", err)
	}
	assertFileNotExists(t, filepath.Join(projectDir, "go.work"))
	assertFileNotExists(t, filepath.Join(projectDir, "deployment", "docker-compose.override.yaml"))
	assertFileNotExists(t, filepath.Join(projectDir, "deployment", "go.work.docker"))

	t.Log("Step 9: Alice runs go mod tidy and compiles all binaries")
	patchGoModReplace(t, projectDir, slingRepoRoot)
	runGoModTidy(t, projectDir)

	runGoBuild(t, projectDir, "./cmd/orders")
	runGoBuild(t, projectDir, "./cmd/billing")
	runGoBuild(t, projectDir, "./cmd/notifications")
	runGoBuild(t, projectDir, "./cmd/migrate")

	t.Log("Step 10: Alice validates template leftovers across the entire project")
	if err := validateLeftovers(projectDir); err != nil {
		t.Fatalf("found template leftovers in generated project: %v", err)
	}

	t.Log("🌟 Golden Flow completed with 100% success!")
}

// TestUserStory_ChaosAndRecovery simulates the Sad Path / Chaos testing journey:
// Bob makes mistakes with invalid names, attempts directory traversal, triggers duplicate collisions,
// tries invalid link paths, and exercises platform concurrency and nil safety under -race.
func TestUserStory_ChaosAndRecovery(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "chaos_proj")

	t.Log("Step 1: Bob passes invalid CLI project names")
	invalidNames := []string{"", "   ", "proj with spaces", "proj/with/slash", "proj!@#$"}
	for _, badName := range invalidNames {
		if err := validateName(badName, "project"); err == nil {
			t.Errorf("expected validateName(%q) to fail, but it succeeded", badName)
		}
	}

	t.Log("Step 2: Bob initializes project successfully")
	if err := initProject(projectDir, "auth"); err != nil {
		t.Fatalf("initProject failed: %v", err)
	}

	t.Log("Step 3: Bob attempts duplicate project initialization in the same folder")
	if err := initProject(projectDir, "another"); err == nil {
		t.Fatal("expected initProject to fail when project directory already exists, but it succeeded")
	}

	t.Log("Step 4: Bob attempts invalid handleAdd targets")
	badTargets := []string{
		"",
		"auth/sub/too/deep",
		"auth/invalid*name",
		"nonexistent_parent/sub",
		"../traversal",
		"/absolute/path",
	}
	for _, badTarget := range badTargets {
		if err := handleAdd(projectDir, badTarget); err == nil {
			t.Errorf("expected handleAdd(%q) to fail, but it succeeded", badTarget)
		}
	}

	t.Log("Step 5: Bob attempts duplicate service and subdomain creation")
	if err := handleAdd(projectDir, "auth"); err == nil {
		t.Fatal("expected handleAdd('auth') to fail for existing service, but it succeeded")
	}

	if err := handleAdd(projectDir, "auth/oauth"); err != nil {
		t.Fatalf("first handleAdd('auth/oauth') failed: %v", err)
	}
	if err := handleAdd(projectDir, "auth/oauth"); err == nil {
		t.Fatal("expected duplicate handleAdd('auth/oauth') to fail, but it succeeded")
	}

	t.Log("Step 6: Bob attempts invalid link operations")
	if err := linkProject(projectDir, filepath.Join(tempDir, "nonexistent_dir_12345")); err == nil {
		t.Fatal("expected linkProject to nonexistent path to fail, but it succeeded")
	}
	if err := linkProject(projectDir, tempDir); err == nil {
		t.Fatal("expected linkProject to non-sling directory to fail, but it succeeded")
	}

	t.Log("Step 7: Bob runs unlink on an unlinked project (idempotency check)")
	if err := unlinkProject(projectDir); err != nil {
		t.Fatalf("unlinkProject failed on unlinked project: %v", err)
	}

	t.Log("Step 8: Bob stress-tests ctxerr concurrency under race conditions")
	ctx := ctxerr.WithSlot(context.Background())
	var wg sync.WaitGroup
	const workers = 100
	wg.Add(workers * 2)
	for i := 0; i < workers; i++ {
		workerID := i
		go func() {
			defer wg.Done()
			ctxerr.SetErr(ctx, fmt.Errorf("chaos error %d", workerID))
		}()
		go func() {
			defer wg.Done()
			_ = ctxerr.Err(ctx)
		}()
	}
	wg.Wait()

	t.Log("💥 Chaos & Recovery completed with 100% success!")
}

var (
	errNotFound     = errors.New("item not found")
	errInvalidTitle = errors.New("invalid item title")
)

// In-memory test repository implementing the template domain.Repository interface
type inMemoryRepo struct {
	mu    sync.RWMutex
	items map[uuid.UUID]testItem
}

type testItem struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
}

func newInMemoryRepo() *inMemoryRepo {
	return &inMemoryRepo{items: make(map[uuid.UUID]testItem)}
}

func (r *inMemoryRepo) Get(_ context.Context, id uuid.UUID) (*testItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.items[id]
	if !ok {
		return nil, errNotFound
	}
	return &item, nil
}

func (r *inMemoryRepo) List(_ context.Context) ([]testItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]testItem, 0, len(r.items))
	for _, item := range r.items {
		list = append(list, item)
	}
	return list, nil
}

func (r *inMemoryRepo) Create(_ context.Context, item *testItem) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[item.ID] = *item
	return nil
}

// TestUserStory_LiveHTTP_PerServiceAndDomain boots in-process HTTP servers and validates
// /v1/items for each service (orders, billing) and domain (fulfillment) across Happy and Sad paths.
func TestUserStory_LiveHTTP_PerServiceAndDomain(t *testing.T) {
	// Setup app configuration
	appCfg := app.Config{
		ServiceName:     "orders-service",
		ShutdownTimeout: 2 * time.Second,
		Logger: logger.Config{
			Format: "text",
			Level:  "error",
		},
		Telemetry: telemetry.Config{
			ServiceName:       "orders-service",
			ServiceVersion:    "1.0.0",
			Environment:       "test",
			PrometheusEnabled: true,
			Interval:          time.Second,
		},
	}

	ordersApp, err := app.New(appCfg.ServiceName, appCfg)
	if err != nil {
		t.Fatalf("failed to create orders app: %v", err)
	}

	// Readiness probe mock
	ordersDBReady := atomic.Bool{}
	ordersDBReady.Store(true)
	ordersApp.Attach("postgres", func(ctx context.Context) error {
		if !ordersDBReady.Load() {
			return errors.New("database connection refused")
		}
		return nil
	})

	// Setup orders domain & fulfillment subdomain in-memory repositories
	ordersRepo := newInMemoryRepo()
	fulfillmentRepo := newInMemoryRepo()

	errorMapping := map[error]int{
		errNotFound:     http.StatusNotFound,
		errInvalidTitle: http.StatusBadRequest,
	}

	// Router setup exactly matching template/service/cmd/main.go
	r := ordersApp.DefaultRouter()

	// 1. Orders Service Routes (/v1/items)
	r.Route("/v1", func(r chi.Router) {
		r.Use(httpx.MapErrors(errorMapping))

		r.Get("/items/{id}", func(w http.ResponseWriter, req *http.Request) {
			id, err := uuid.Parse(chi.URLParam(req, "id"))
			if err != nil {
				httpx.BadRequest(w, req, "invalid item id")
				return
			}
			item, err := ordersRepo.Get(req.Context(), id)
			if err != nil {
				httpx.WriteError(w, req, err)
				return
			}
			httpx.JSON(w, http.StatusOK, item)
		})

		r.Get("/items", func(w http.ResponseWriter, req *http.Request) {
			items, _ := ordersRepo.List(req.Context())
			httpx.JSON(w, http.StatusOK, items)
		})

		r.Post("/items", func(w http.ResponseWriter, req *http.Request) {
			var body struct {
				Title string `json:"title"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				httpx.BadRequest(w, req, "invalid request body")
				return
			}
			if len(body.Title) == 0 {
				httpx.WriteError(w, req, errInvalidTitle)
				return
			}
			item := &testItem{
				ID:        uuid.NewV7(),
				Title:     body.Title,
				CreatedAt: time.Now(),
			}
			_ = ordersRepo.Create(req.Context(), item)
			httpx.JSON(w, http.StatusCreated, item)
		})
	})

	// 2. Fulfillment Subdomain Routes (/v1/fulfillment/items)
	r.Route("/v1/fulfillment", func(r chi.Router) {
		r.Use(httpx.MapErrors(errorMapping))

		r.Post("/items", func(w http.ResponseWriter, req *http.Request) {
			var body struct {
				Title string `json:"title"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				httpx.BadRequest(w, req, "invalid request body")
				return
			}
			if len(body.Title) == 0 {
				httpx.WriteError(w, req, errInvalidTitle)
				return
			}
			item := &testItem{
				ID:        uuid.NewV7(),
				Title:     body.Title,
				CreatedAt: time.Now(),
			}
			_ = fulfillmentRepo.Create(req.Context(), item)
			httpx.JSON(w, http.StatusCreated, item)
		})

		r.Get("/items/{id}", func(w http.ResponseWriter, req *http.Request) {
			id, err := uuid.Parse(chi.URLParam(req, "id"))
			if err != nil {
				httpx.BadRequest(w, req, "invalid item id")
				return
			}
			item, err := fulfillmentRepo.Get(req.Context(), id)
			if err != nil {
				httpx.WriteError(w, req, err)
				return
			}
			httpx.JSON(w, http.StatusOK, item)
		})
	})

	// Start server on an ephemeral port
	port := getFreePort(t)
	ordersApp.ServeHTTP(fmt.Sprintf("127.0.0.1:%d", port), r)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrChan := make(chan error, 1)
	go func() {
		runErrChan <- ordersApp.Run(ctx)
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForServer(t, baseURL+"/healthz", 2*time.Second)

	client := &http.Client{Timeout: 2 * time.Second}

	// -------------------------------------------------------------
	// 🌟 HAPPY PATH: Orders Service & Fulfillment Subdomain
	// -------------------------------------------------------------
	t.Log("Happy Path: checking /healthz probe")
	resp, err := client.Get(baseURL + "/healthz")
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusOK)
	assertEqual(t, readBody(t, resp), "ok\n")

	t.Log("Happy Path: checking /readyz probe")
	resp, err = client.Get(baseURL + "/readyz")
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusOK)
	assertEqual(t, readBody(t, resp), "ready\n")

	t.Log("Happy Path: POST /v1/items on Orders service")
	postReqBody, _ := json.Marshal(map[string]string{"title": "Order Alpha"})
	resp, err = client.Post(baseURL+"/v1/items", "application/json", bytes.NewReader(postReqBody))
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusCreated)

	var createdOrder testItem
	_ = json.Unmarshal([]byte(readBody(t, resp)), &createdOrder)
	if createdOrder.ID == uuid.Nil() {
		t.Fatal("expected valid UUIDv7 for created order, got nil UUID")
	}
	assertEqual(t, createdOrder.Title, "Order Alpha")

	t.Log("Happy Path: GET /v1/items/{id} on Orders service")
	resp, err = client.Get(fmt.Sprintf("%s/v1/items/%s", baseURL, createdOrder.ID.String()))
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusOK)
	var fetchedOrder testItem
	_ = json.Unmarshal([]byte(readBody(t, resp)), &fetchedOrder)
	assertEqual(t, fetchedOrder.ID, createdOrder.ID)
	assertEqual(t, fetchedOrder.Title, "Order Alpha")

	t.Log("Happy Path: GET /v1/items list on Orders service")
	resp, err = client.Get(baseURL + "/v1/items")
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusOK)
	var ordersList []testItem
	_ = json.Unmarshal([]byte(readBody(t, resp)), &ordersList)
	if len(ordersList) != 1 {
		t.Fatalf("expected 1 order in list, got %d", len(ordersList))
	}

	t.Log("Happy Path: POST /v1/fulfillment/items on Fulfillment subdomain")
	postSubBody, _ := json.Marshal(map[string]string{"title": "Shipment Package #1"})
	resp, err = client.Post(baseURL+"/v1/fulfillment/items", "application/json", bytes.NewReader(postSubBody))
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusCreated)

	var createdShipment testItem
	_ = json.Unmarshal([]byte(readBody(t, resp)), &createdShipment)
	assertEqual(t, createdShipment.Title, "Shipment Package #1")

	t.Log("Happy Path: GET /v1/fulfillment/items/{id} on Fulfillment subdomain")
	resp, err = client.Get(fmt.Sprintf("%s/v1/fulfillment/items/%s", baseURL, createdShipment.ID.String()))
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusOK)

	t.Log("Happy Path: GET /metrics contains Prometheus metrics")
	resp, err = client.Get(baseURL + "/metrics")
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusOK)
	metricsBody := readBody(t, resp)
	if !strings.Contains(metricsBody, "http_server_requests_total") {
		t.Fatalf("metrics body does not contain http_server_requests_total:\n%s", metricsBody)
	}

	// -------------------------------------------------------------
	// 🌟 BILLING SERVICE: Independent Service /v1/items verification
	// -------------------------------------------------------------
	t.Log("Happy Path: Testing independent Billing Service /v1/items")
	billingAppCfg := app.Config{
		ServiceName:     "billing-service",
		ShutdownTimeout: 2 * time.Second,
		Logger: logger.Config{
			Format: "text",
			Level:  "error",
		},
		Telemetry: telemetry.Config{
			ServiceName:       "billing-service",
			ServiceVersion:    "1.0.0",
			Environment:       "test",
			PrometheusEnabled: true,
			Interval:          time.Second,
		},
	}
	billingApp, err := app.New(billingAppCfg.ServiceName, billingAppCfg)
	if err != nil {
		t.Fatalf("failed to create billing app: %v", err)
	}

	billingRepo := newInMemoryRepo()
	billingRouter := billingApp.DefaultRouter()
	billingRouter.Route("/v1", func(r chi.Router) {
		r.Use(httpx.MapErrors(errorMapping))

		r.Get("/items/{id}", func(w http.ResponseWriter, req *http.Request) {
			id, err := uuid.Parse(chi.URLParam(req, "id"))
			if err != nil {
				httpx.BadRequest(w, req, "invalid item id")
				return
			}
			item, err := billingRepo.Get(req.Context(), id)
			if err != nil {
				httpx.WriteError(w, req, err)
				return
			}
			httpx.JSON(w, http.StatusOK, item)
		})

		r.Post("/items", func(w http.ResponseWriter, req *http.Request) {
			var body struct {
				Title string `json:"title"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				httpx.BadRequest(w, req, "invalid request body")
				return
			}
			if len(body.Title) == 0 {
				httpx.WriteError(w, req, errInvalidTitle)
				return
			}
			item := &testItem{
				ID:        uuid.NewV7(),
				Title:     body.Title,
				CreatedAt: time.Now(),
			}
			_ = billingRepo.Create(req.Context(), item)
			httpx.JSON(w, http.StatusCreated, item)
		})
	})

	billingPort := getFreePort(t)
	billingApp.ServeHTTP(fmt.Sprintf("127.0.0.1:%d", billingPort), billingRouter)

	billingCtx, billingCancel := context.WithCancel(context.Background())
	defer billingCancel()
	billingRunErrChan := make(chan error, 1)
	go func() {
		billingRunErrChan <- billingApp.Run(billingCtx)
	}()

	billingBaseURL := fmt.Sprintf("http://127.0.0.1:%d", billingPort)
	waitForServer(t, billingBaseURL+"/healthz", 2*time.Second)

	postBillingBody, _ := json.Marshal(map[string]string{"title": "Invoice #909"})
	resp, err = client.Post(billingBaseURL+"/v1/items", "application/json", bytes.NewReader(postBillingBody))
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusCreated)

	var createdInvoice testItem
	_ = json.Unmarshal([]byte(readBody(t, resp)), &createdInvoice)
	assertEqual(t, createdInvoice.Title, "Invoice #909")

	// Verify isolation: Invoice #909 does NOT exist in orders service
	resp, err = client.Get(fmt.Sprintf("%s/v1/items/%s", baseURL, createdInvoice.ID.String()))
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusNotFound)
	_ = readBody(t, resp)

	// Clean graceful shutdown of billingApp
	billingCancel()
	select {
	case err := <-billingRunErrChan:
		assertNoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("billing app failed to shutdown within timeout")
	}

	// -------------------------------------------------------------
	// 💥 SAD PATH: Orders Service & Fulfillment Subdomain
	// -------------------------------------------------------------
	t.Log("Sad Path: POST /v1/items with empty title returns 400 Bad Request")
	emptyTitleBody, _ := json.Marshal(map[string]string{"title": ""})
	resp, err = client.Post(baseURL+"/v1/items", "application/json", bytes.NewReader(emptyTitleBody))
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusBadRequest)
	if !strings.Contains(readBody(t, resp), "invalid item title") {
		t.Fatal("expected error message 'invalid item title' in 400 response")
	}

	t.Log("Sad Path: POST /v1/items with malformed JSON returns 400 Bad Request")
	resp, err = client.Post(baseURL+"/v1/items", "application/json", strings.NewReader(`{"title": broken json`))
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusBadRequest)
	_ = readBody(t, resp)

	t.Log("Sad Path: GET /v1/items/not-a-uuid returns 400 Bad Request")
	resp, err = client.Get(baseURL + "/v1/items/not-a-uuid")
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusBadRequest)
	_ = readBody(t, resp)

	t.Log("Sad Path: GET /v1/items/{nonexistent_id} returns 404 Not Found")
	nonExistentID := uuid.NewV7()
	resp, err = client.Get(fmt.Sprintf("%s/v1/items/%s", baseURL, nonExistentID.String()))
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusNotFound)
	if !strings.Contains(readBody(t, resp), "item not found") {
		t.Fatal("expected error message 'item not found' in 404 response")
	}

	t.Log("Sad Path: GET /unknown-route returns 404 Not Found")
	resp, err = client.Get(baseURL + "/random-unregistered-route")
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusNotFound)
	_ = readBody(t, resp)

	t.Log("Sad Path: /readyz probe returns 503 when dependency fails")
	ordersDBReady.Store(false)
	resp, err = client.Get(baseURL + "/readyz")
	assertNoError(t, err)
	assertEqual(t, resp.StatusCode, http.StatusServiceUnavailable)
	_ = readBody(t, resp)

	// Clean graceful shutdown of ordersApp
	cancel()
	select {
	case err := <-runErrChan:
		assertNoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("app failed to shutdown within timeout")
	}

	t.Log("✅ Live HTTP Runtime E2E verification completed with 100% success!")
}

// Helpers needed for tests
func getFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitForServer(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{
		Timeout: 100 * time.Millisecond,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become ready within %v", url, timeout)
}

func assertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	return string(bytes)
}
