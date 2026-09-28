//go:build e2e

package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"uuid"
)

// TestCustomerZeroSmoke verifies a full Sling cluster end-to-end:
// scaffolding, compose boot, gateway routing, DB isolation, Air hot-reload, telemetry, and teardown.
func TestCustomerZeroSmoke(t *testing.T) {
	tempDir := t.TempDir()
	projectName := "e2e-app"
	projectDir := filepath.Join(tempDir, projectName)
	slingRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("failed to resolve sling root: %v", err)
	}

	if err := os.Symlink(slingRoot, filepath.Join(tempDir, "sling")); err != nil {
		t.Fatalf("failed to symlink sling repo: %v", err)
	}

	os.Setenv("SLING_TEMPLATE_DIR", filepath.Join(slingRoot, "template"))
	os.Setenv("UID", fmt.Sprintf("%d", os.Getuid()))
	os.Setenv("GID", fmt.Sprintf("%d", os.Getgid()))

	slingBinary := buildSlingCLI(t, tempDir, slingRoot)

	// Phase 1: Scaffolding and pre-flight compilation.
	t.Log("phase 1: scaffolding cluster (booking + billing + subdomains)")
	runCmd(t, tempDir, slingBinary, "init", projectDir, "booking")
	runCmd(t, projectDir, slingBinary, "add", "billing")
	runCmd(t, projectDir, slingBinary, "add", "booking/analytics")
	runCmd(t, projectDir, slingBinary, "add", "billing/analytics")

	placeholderRegex := regexp.MustCompile(`__[A-Z0-9_]+__`)
	err = filepath.Walk(projectDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if strings.Contains(path, ".git") || strings.HasSuffix(path, ".sum") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if matches := placeholderRegex.FindAll(data, -1); len(matches) > 0 {
			var matchStrings []string
			for _, m := range matches {
				matchStrings = append(matchStrings, string(m))
			}
			return fmt.Errorf("unresolved placeholders in %s: %v", path, matchStrings)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("template placeholder check failed: %v", err)
	}

	migrateMainPath := filepath.Join(projectDir, "cmd", "migrate", "main.go")
	migrateContent, err := os.ReadFile(migrateMainPath)
	if err != nil {
		t.Fatalf("failed to read %s: %v", migrateMainPath, err)
	}
	migrateText := string(migrateContent)
	if !strings.Contains(migrateText, "booking_analytics") {
		t.Fatalf("expected cmd/migrate/main.go to contain alias 'booking_analytics', got:\n%s", migrateText)
	}
	if !strings.Contains(migrateText, "billing_analytics") {
		t.Fatalf("expected cmd/migrate/main.go to contain alias 'billing_analytics', got:\n%s", migrateText)
	}

	runCmd(t, projectDir, slingBinary, "link", slingRoot)
	patchGoMod(t, projectDir, slingRoot)
	runCmd(t, projectDir, "go", "mod", "tidy")
	unpatchGoMod(t, projectDir, slingRoot)

	runCmd(t, projectDir, "go", "build", "-o", "/dev/null", "./cmd/booking")
	runCmd(t, projectDir, "go", "build", "-o", "/dev/null", "./cmd/billing")
	runCmd(t, projectDir, "go", "build", "-o", "/dev/null", "./cmd/migrate")

	// Phase 2: Docker Compose boot and race gate.
	t.Log("phase 2: booting docker compose stack")
	composeProject := fmt.Sprintf("e2e_%d", time.Now().UnixNano()%1000000000)
	deploymentDir := filepath.Join(projectDir, "deployment")
	composeEnv := []string{
		fmt.Sprintf("COMPOSE_PROJECT_NAME=%s", composeProject),
		fmt.Sprintf("UID=%d", os.Getuid()),
		fmt.Sprintf("GID=%d", os.Getgid()),
	}

	defer func() {
		if t.Failed() {
			t.Log("--- dumping compose logs on failure ---")
			out, _ := exec.Command("docker", "compose", "-p", composeProject, "--project-directory", deploymentDir, "logs", "--no-color").CombinedOutput()
			t.Log(string(out))
		}

		// Phase 7: Teardown and leak check.
		t.Log("phase 7: tearing down stack and checking cleanliness")
		_ = exec.Command("docker", "compose", "-p", composeProject, "--project-directory", deploymentDir, "down", "-v", "--remove-orphans").Run()
		_ = exec.Command("docker", "run", "--rm", "-v", tempDir+":/work", "alpine", "rm", "-rf", "/work/"+projectName+"/tmp").Run()

		checkHostCleanliness(t, composeProject)
	}()

	runCmdWithEnv(t, projectDir, composeEnv, "docker", "compose", "-p", composeProject, "--project-directory", deploymentDir, "up", "-d", "--build")

	waitForContainerExitZero(t, composeProject, "postgres-init", 50*time.Second)
	waitForContainerExitZero(t, composeProject, "booking-migrator", 50*time.Second)
	waitForContainerExitZero(t, composeProject, "billing-migrator", 50*time.Second)

	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}

	waitForCondition(t, 45*time.Second, "booking service readiness", func() bool {
		resp, err := client.Get("http://localhost/api/booking/readyz")
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			return strings.Contains(string(body), "ready")
		}
		return false
	})

	waitForCondition(t, 45*time.Second, "billing service readiness", func() bool {
		resp, err := client.Get("http://localhost/api/billing/readyz")
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			return strings.Contains(string(body), "ready")
		}
		return false
	})

	// Phase 3: Gateway routing and CORS.
	t.Log("phase 3: gateway routing, prefix stripping, and CORS")
	corsReq, err := http.NewRequest(http.MethodOptions, "http://localhost/api/booking/v1/items", nil)
	assertNoError(t, err)
	corsReq.Header.Set("Origin", "http://localhost:3000")
	corsReq.Header.Set("Access-Control-Request-Method", "POST")
	corsResp, err := client.Do(corsReq)
	assertNoError(t, err)
	assertEqual(t, corsResp.StatusCode, http.StatusNoContent)
	assertEqual(t, corsResp.Header.Get("Access-Control-Allow-Origin"), "*")
	allowHeaders := corsResp.Header.Get("Access-Control-Allow-Headers")
	if !strings.Contains(allowHeaders, "traceparent") {
		t.Fatalf("expected Access-Control-Allow-Headers to contain 'traceparent', got %q", allowHeaders)
	}

	healthResp, err := client.Get("http://localhost/api/booking/healthz")
	assertNoError(t, err)
	assertEqual(t, healthResp.StatusCode, http.StatusOK)
	healthBody, _ := io.ReadAll(healthResp.Body)
	_ = healthResp.Body.Close()
	if !strings.Contains(string(healthBody), "ok") {
		t.Fatalf("expected /healthz body to contain 'ok', got %q", string(healthBody))
	}

	ghostClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}
	startTimer := time.Now()
	ghostResp, err := ghostClient.Get("http://localhost/api/ghost_service/v1/items")
	elapsed := time.Since(startTimer)
	assertNoError(t, err)
	if ghostResp.StatusCode != http.StatusNotFound && ghostResp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 404 or 502 for nonexistent service, got %d", ghostResp.StatusCode)
	}
	if elapsed > 4*time.Second {
		t.Fatalf("gateway took too long (%v) to fail fast on nonexistent service (expected < 4s)", elapsed)
	}
	_ = ghostResp.Body.Close()

	// Phase 4: Persistence, UUIDv7, and data isolation.
	t.Log("phase 4: persistence, UUIDv7, and database isolation")
	createPayload := []byte(`{"title":"VIP Room"}`)
	createResp, err := client.Post("http://localhost/api/booking/v1/items", "application/json", bytes.NewReader(createPayload))
	assertNoError(t, err)
	assertEqual(t, createResp.StatusCode, http.StatusCreated)

	var bookingItem struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		CreatedAt string `json:"created_at"`
	}
	decodeJSON(t, createResp, &bookingItem)
	assertEqual(t, bookingItem.Title, "VIP Room")

	u, err := uuid.Parse(bookingItem.ID)
	if err != nil {
		t.Fatalf("failed to parse returned item ID as UUID: %v", err)
	}
	uuidVersion := (u[6] >> 4) & 0x0f
	if uuidVersion != 7 {
		t.Fatalf("expected UUID version 7, got version %d for ID %s", uuidVersion, bookingItem.ID)
	}
	tsMillis := int64(u[0])<<40 | int64(u[1])<<32 | int64(u[2])<<24 | int64(u[3])<<16 | int64(u[4])<<8 | int64(u[5])
	nowMillis := time.Now().UnixMilli()
	diff := nowMillis - tsMillis
	if diff < 0 {
		diff = -diff
	}
	if diff > 60000 {
		t.Fatalf("UUIDv7 timestamp drift too large: generated %d vs now %d (diff: %dms)", tsMillis, nowMillis, diff)
	}

	if _, err := time.Parse(time.RFC3339Nano, bookingItem.CreatedAt); err != nil {
		if _, err2 := time.Parse(time.RFC3339, bookingItem.CreatedAt); err2 != nil {
			t.Fatalf("invalid created_at timestamp %q: %v", bookingItem.CreatedAt, err)
		}
	}

	getResp, err := client.Get("http://localhost/api/booking/v1/items/" + bookingItem.ID)
	assertNoError(t, err)
	assertEqual(t, getResp.StatusCode, http.StatusOK)
	var readItem struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	decodeJSON(t, getResp, &readItem)
	assertEqual(t, readItem.ID, bookingItem.ID)
	assertEqual(t, readItem.Title, "VIP Room")

	crossResp, err := client.Get("http://localhost/api/billing/v1/items/" + bookingItem.ID)
	assertNoError(t, err)
	assertEqual(t, crossResp.StatusCode, http.StatusNotFound)
	var crossErr struct {
		Error string `json:"error"`
	}
	decodeJSON(t, crossResp, &crossErr)
	if !strings.Contains(crossErr.Error, "item not found") {
		t.Fatalf("expected 'item not found' error from billing, got %q", crossErr.Error)
	}

	checkTableExists(t, composeProject, deploymentDir, "booking_db", "items")
	checkTableExists(t, composeProject, deploymentDir, "booking_db", "analytics_items")
	checkTableExists(t, composeProject, deploymentDir, "billing_db", "items")
	checkTableExists(t, composeProject, deploymentDir, "billing_db", "analytics_items")

	badTitleResp, err := client.Post("http://localhost/api/booking/v1/items", "application/json", bytes.NewReader([]byte(`{"title":""}`)))
	assertNoError(t, err)
	assertEqual(t, badTitleResp.StatusCode, http.StatusBadRequest)
	var badTitleErr struct {
		Error string `json:"error"`
	}
	decodeJSON(t, badTitleResp, &badTitleErr)
	if !strings.Contains(badTitleErr.Error, "invalid item title") {
		t.Fatalf("expected 'invalid item title' error, got %q", badTitleErr.Error)
	}

	badUUIDResp, err := client.Get("http://localhost/api/booking/v1/items/not-a-uuid")
	assertNoError(t, err)
	assertEqual(t, badUUIDResp.StatusCode, http.StatusBadRequest)
	var badUUIDErr struct {
		Error string `json:"error"`
	}
	decodeJSON(t, badUUIDResp, &badUUIDErr)
	if !strings.Contains(badUUIDErr.Error, "invalid item id") {
		t.Fatalf("expected 'invalid item id' error, got %q", badUUIDErr.Error)
	}

	missingUUIDResp, err := client.Get("http://localhost/api/booking/v1/items/018f0000-0000-7000-8000-000000000000")
	assertNoError(t, err)
	assertEqual(t, missingUUIDResp.StatusCode, http.StatusNotFound)
	var missingErr struct {
		Error string `json:"error"`
	}
	decodeJSON(t, missingUUIDResp, &missingErr)
	if !strings.Contains(missingErr.Error, "item not found") {
		t.Fatalf("expected 'item not found' error, got %q", missingErr.Error)
	}

	// Phase 5: Air hot-reload test.
	t.Log("phase 5: air hot-reload test")
	domainFilePath := filepath.Join(projectDir, "internal", "booking", "domain.go")
	existingContent, err := os.ReadFile(domainFilePath)
	assertNoError(t, err)

	hotReloadComment := fmt.Sprintf("\n// Hot-reload verification timestamp: %s\n", time.Now().Format(time.RFC3339Nano))
	updatedContent := append(existingContent, []byte(hotReloadComment)...)
	err = os.WriteFile(domainFilePath, updatedContent, 0644)
	assertNoError(t, err)

	time.Sleep(4 * time.Second)

	bookingContainerID := getContainerID(t, composeProject, "booking")
	inspectCmd := exec.Command("docker", "inspect", "--format", "{{.State.Running}} {{.State.ExitCode}}", bookingContainerID)
	inspectOutput, err := inspectCmd.Output()
	assertNoError(t, err)
	fields := strings.Fields(string(inspectOutput))
	if len(fields) < 1 || fields[0] != "true" {
		t.Fatalf("booking container died during Air hot-reload: status=%s", string(inspectOutput))
	}

	reloadResp, err := client.Get("http://localhost/api/booking/healthz")
	assertNoError(t, err)
	assertEqual(t, reloadResp.StatusCode, http.StatusOK)
	_ = reloadResp.Body.Close()

	// Phase 6: Observability pipeline (Prometheus, Jaeger, Loki).
	t.Log("phase 6: observability verification (prometheus, jaeger, loki)")

	postReloadItemResp, err := client.Get("http://localhost/api/booking/v1/items/" + bookingItem.ID)
	assertNoError(t, err)
	assertEqual(t, postReloadItemResp.StatusCode, http.StatusOK)
	_ = postReloadItemResp.Body.Close()

	promURL := "http://localhost:9090/api/v1/query?query=http_server_requests_total"
	var promData struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  []any             `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}

	waitForCondition(t, 25*time.Second, "Prometheus metric scraping", func() bool {
		r, err := client.Get(promURL)
		if err != nil || r.StatusCode != http.StatusOK {
			return false
		}
		decodeJSON(t, r, &promData)
		if promData.Status != "success" || len(promData.Data.Result) == 0 {
			return false
		}
		for _, res := range promData.Data.Result {
			if strings.Contains(res.Metric["service"], "booking") {
				route := res.Metric["route"]
				if strings.Contains(route, bookingItem.ID) {
					t.Fatalf("cardinality leak: Prometheus route contains raw UUID: %s", route)
				}
				if strings.Contains(route, "/v1/items") {
					return true
				}
			}
		}
		return false
	})

	now := time.Now()
	startTimeMin := now.Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	startTimeMax := now.Add(1 * time.Hour).UTC().Format(time.RFC3339)
	jaegerV3URL := fmt.Sprintf("http://localhost:16686/api/v3/traces?query.serviceName=booking&query.startTimeMin=%s&query.startTimeMax=%s",
		url.QueryEscape(startTimeMin), url.QueryEscape(startTimeMax))
	jaegerV1URL := "http://localhost:16686/api/traces?service=booking&limit=5"

	var traceID string
	waitForCondition(t, 25*time.Second, "Jaeger trace collection", func() bool {
		// Modern Jaeger v2/v3 endpoint
		if r, err := client.Get(jaegerV3URL); err == nil && r.StatusCode == http.StatusOK {
			var v3Data struct {
				Result struct {
					ResourceSpans []struct {
						ScopeSpans []struct {
							Spans []struct {
								TraceID string `json:"traceId"`
							} `json:"spans"`
						} `json:"scopeSpans"`
					} `json:"resourceSpans"`
				} `json:"result"`
			}
			decodeJSON(t, r, &v3Data)
			for _, rs := range v3Data.Result.ResourceSpans {
				for _, ss := range rs.ScopeSpans {
					if len(ss.Spans) > 0 && ss.Spans[0].TraceID != "" {
						traceID = ss.Spans[0].TraceID
						return true
					}
				}
			}
		}

		// Fallback to legacy v1 endpoint for backward compatibility
		if r, err := client.Get(jaegerV1URL); err == nil && r.StatusCode == http.StatusOK {
			var v1Data struct {
				Data []struct {
					TraceID string `json:"traceID"`
					Spans   []struct {
						OperationName string `json:"operationName"`
					} `json:"spans"`
				} `json:"data"`
			}
			decodeJSON(t, r, &v1Data)
			if len(v1Data.Data) > 0 && len(v1Data.Data[0].Spans) > 0 {
				traceID = v1Data.Data[0].TraceID
				return true
			}
		}

		return false
	})

	lokiQuery := `{service="booking"}`
	if traceID != "" {
		lokiQuery = fmt.Sprintf(`{service="booking"} |= "%s"`, traceID)
	}

	var lokiData struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Stream map[string]string `json:"stream"`
				Values [][]string        `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}

	lokiURL := fmt.Sprintf("http://localhost:3100/loki/api/v1/query_range?query=%s", url.QueryEscape(lokiQuery))
	waitForCondition(t, 45*time.Second, "Loki log collection", func() bool {
		r, err := client.Get(lokiURL)
		if err != nil || r.StatusCode != http.StatusOK {
			return false
		}
		decodeJSON(t, r, &lokiData)
		return lokiData.Status == "success" && len(lokiData.Data.Result) > 0
	})
}

func buildSlingCLI(t *testing.T, tempDir, slingRoot string) string {
	t.Helper()
	binPath := filepath.Join(tempDir, "sling_bin")
	cmd := exec.Command("go", "build", "-o", binPath, filepath.Join(slingRoot, "cmd", "sling"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build sling CLI: %v\n%s", err, string(out))
	}
	return binPath
}

func patchGoMod(t *testing.T, projectDir, slingAbsPath string) {
	t.Helper()
	p := filepath.Join(projectDir, "go.mod")
	b, _ := os.ReadFile(p)
	s := string(b) + "\nreplace github.com/blessed-go/sling => " + slingAbsPath + "\n"
	_ = os.WriteFile(p, []byte(s), 0644)
}

func unpatchGoMod(t *testing.T, projectDir, slingAbsPath string) {
	t.Helper()
	p := filepath.Join(projectDir, "go.mod")
	b, _ := os.ReadFile(p)
	s := strings.ReplaceAll(string(b), "\nreplace github.com/blessed-go/sling => "+slingAbsPath+"\n", "")
	_ = os.WriteFile(p, []byte(s), 0644)
}

func runCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("command failed: %s %v\nOutput: %s\nErr: %v", name, args, string(out), err)
	}
}

func runCmdWithEnv(t *testing.T, dir string, env []string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("command failed: %s %v\nOutput: %s\nErr: %v", name, args, string(out), err)
	}
}

func getContainerID(t *testing.T, composeProject, serviceName string) string {
	t.Helper()
	cmd := exec.Command("docker", "ps", "-a",
		"--filter", "label=com.docker.compose.project="+composeProject,
		"--filter", "label=com.docker.compose.service="+serviceName,
		"--format", "{{.ID}}",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to query container ID for [%s]: %v", serviceName, err)
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		t.Fatalf("no container found for service [%s] in project [%s]", serviceName, composeProject)
	}
	return id
}

func waitForContainerExitZero(t *testing.T, composeProject, serviceName string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cmd := exec.Command("docker", "ps", "-a",
			"--filter", "label=com.docker.compose.project="+composeProject,
			"--filter", "label=com.docker.compose.service="+serviceName,
			"--format", "{{.ID}}",
		)
		out, err := cmd.Output()
		if err == nil {
			id := strings.TrimSpace(string(out))
			if id != "" {
				inspectCmd := exec.Command("docker", "inspect",
					"--format", "{{.State.Status}} {{.State.ExitCode}}",
					id,
				)
				inspOut, inspErr := inspectCmd.Output()
				if inspErr == nil {
					fields := strings.Fields(string(inspOut))
					if len(fields) >= 2 {
						status := fields[0]
						exitCode := fields[1]
						if status == "exited" {
							if exitCode == "0" {
								return
							}
							logOut, _ := exec.Command("docker", "logs", "--no-color", id).CombinedOutput()
							t.Fatalf("container [%s] failed with exit code %s:\n%s", serviceName, exitCode, string(logOut))
						}
					}
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for container [%s] to exit with code 0", serviceName)
}

func checkTableExists(t *testing.T, composeProject, deploymentDir, dbName, tableName string) {
	t.Helper()
	cmd := exec.Command("docker", "compose", "-p", composeProject, "--project-directory", deploymentDir,
		"exec", "-T", "postgres",
		"psql", "-U", "postgres", "-d", dbName, "-tAc",
		fmt.Sprintf("SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name='%s'", tableName),
	)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "1") {
		t.Fatalf("expected table %q to exist in database %q, but query returned: %s (err: %v)", tableName, dbName, string(out), err)
	}
}

func checkHostCleanliness(t *testing.T, composeProject string) {
	t.Helper()
	cmd := exec.Command("docker", "ps", "-a", "--filter", "label=com.docker.compose.project="+composeProject, "--format", "{{.ID}}")
	out, _ := cmd.Output()
	ids := strings.Fields(string(out))
	if len(ids) > 0 {
		t.Errorf("leaked containers for project %s: %v", composeProject, ids)
		for _, id := range ids {
			_ = exec.Command("docker", "rm", "-f", id).Run()
		}
	}

	volCmd := exec.Command("docker", "volume", "ls", "--filter", "label=com.docker.compose.project="+composeProject, "--format", "{{.Name}}")
	volOut, _ := volCmd.Output()
	vols := strings.Fields(string(volOut))
	if len(vols) > 0 {
		t.Errorf("leaked volumes for project %s: %v", composeProject, vols)
		for _, vol := range vols {
			_ = exec.Command("docker", "volume", "rm", "-f", vol).Run()
		}
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("timed out waiting for condition: %s", desc)
}

func decodeJSON(t *testing.T, resp *http.Response, target any) {
	t.Helper()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, target); err != nil {
		t.Fatalf("failed to decode JSON response: %v\nRaw body: %s", err, string(body))
	}
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
