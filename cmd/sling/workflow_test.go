package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	if version != "v0.1.1" {
		t.Errorf("expected version to be 'v0.1.1', got %q", version)
	}
}

// TestWorkflow_MultiService verifies end-to-end scaffolding of a multi-service workspace,
// including subdomain creation, table/cache isolation, local linking, and clean compilation.
func TestWorkflow_MultiService(t *testing.T) {
	tempDir := t.TempDir()
	projectName := "store"
	projectDir := filepath.Join(tempDir, projectName)

	slingRepoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("failed to resolve sling repo root: %v", err)
	}

	t.Log("verifying CLI version")
	if version != "v0.1.1" {
		t.Fatalf("expected version v0.1.1, got %s", version)
	}

	t.Log("initializing project workspace with first service 'orders'")
	if err := initProject(projectDir, "orders"); err != nil {
		t.Fatalf("initProject failed: %v", err)
	}
	assertFileExists(t, filepath.Join(projectDir, "go.mod"))
	assertFileExists(t, filepath.Join(projectDir, "Taskfile.yml"))
	assertFileExists(t, filepath.Join(projectDir, "cmd", "orders", "main.go"))
	assertFileExists(t, filepath.Join(projectDir, "internal", "orders", "domain.go"))
	assertFileExists(t, filepath.Join(projectDir, "conf", "orders.toml"))
	assertFileExists(t, filepath.Join(projectDir, ".gitignore"))
	assertFileExists(t, filepath.Join(projectDir, ".dockerignore"))
	assertFileExists(t, filepath.Join(projectDir, ".env.example"))

	t.Log("adding subdomain 'fulfillment' inside service 'orders'")
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

	t.Log("adding independent microservice 'billing'")
	if err := handleAdd(projectDir, "billing"); err != nil {
		t.Fatalf("handleAdd for billing failed: %v", err)
	}
	assertFileExists(t, filepath.Join(projectDir, "cmd", "billing", "main.go"))
	assertFileExists(t, filepath.Join(projectDir, "internal", "billing", "domain.go"))
	assertFileExists(t, filepath.Join(projectDir, "conf", "billing.toml"))

	composeContent := readFile(t, filepath.Join(projectDir, "deployment", "docker-compose.yaml"))
	if !strings.Contains(composeContent, "billing.compose.yaml") {
		t.Fatalf("billing.compose.yaml not registered in docker-compose.yaml include list:\n%s", composeContent)
	}

	ordersCompose := readFile(t, filepath.Join(projectDir, "deployment", "orders.compose.yaml"))
	if !strings.Contains(ordersCompose, "2345:2345") {
		t.Fatalf("orders.compose.yaml missing delve port 2345:\n%s", ordersCompose)
	}
	billingCompose := readFile(t, filepath.Join(projectDir, "deployment", "billing.compose.yaml"))
	if !strings.Contains(billingCompose, "2346:2345") {
		t.Fatalf("billing.compose.yaml missing delve port 2346:\n%s", billingCompose)
	}

	t.Log("adding subdomain 'fulfillment' to service 'billing' (namespace collision check)")
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

	t.Log("linking local Sling platform repository")
	if err := linkProject(projectDir, slingRepoRoot); err != nil {
		t.Fatalf("linkProject failed: %v", err)
	}
	assertFileExists(t, filepath.Join(projectDir, "go.work"))
	assertFileExists(t, filepath.Join(projectDir, "deployment", "docker-compose.override.yaml"))
	assertFileExists(t, filepath.Join(projectDir, "deployment", "go.work.docker"))

	t.Log("adding service 'notifications' while workspace is linked")
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

	t.Log("unlinking Sling platform")
	if err := unlinkProject(projectDir); err != nil {
		t.Fatalf("unlinkProject failed: %v", err)
	}
	assertFileNotExists(t, filepath.Join(projectDir, "go.work"))
	assertFileNotExists(t, filepath.Join(projectDir, "deployment", "docker-compose.override.yaml"))
	assertFileNotExists(t, filepath.Join(projectDir, "deployment", "go.work.docker"))

	t.Log("running go mod tidy and compiling all service binaries")
	patchGoModReplace(t, projectDir, slingRepoRoot)
	runGoModTidy(t, projectDir)

	runGoBuild(t, projectDir, "./cmd/orders")
	runGoBuild(t, projectDir, "./cmd/billing")
	runGoBuild(t, projectDir, "./cmd/notifications")
	runGoBuild(t, projectDir, "./cmd/migrate")

	t.Log("validating template placeholder cleanup across generated project")
	if err := validateLeftovers(projectDir); err != nil {
		t.Fatalf("found template leftovers in generated project: %v", err)
	}
}

// TestWorkflow_CLI_Validation verifies CLI validation and edge cases:
// empty projects without initial services, invalid names, duplicate project/service initialization,
// path traversal prevention, invalid link paths, and idempotency of unlink.
func TestWorkflow_CLI_Validation(t *testing.T) {
	tempDir := t.TempDir()

	t.Log("verifying rejection of invalid project names")
	invalidNames := []string{"", "   ", "proj with spaces", "proj/with/slash", "proj!@#$"}
	for _, badName := range invalidNames {
		if err := validateName(badName, "project"); err == nil {
			t.Errorf("expected validateName(%q) to fail, but it succeeded", badName)
		}
	}

	t.Log("initializing bare project without initial service")
	bareProjectDir := filepath.Join(tempDir, "bare_proj")
	if err := initProject(bareProjectDir, ""); err != nil {
		t.Fatalf("initProject without service failed: %v", err)
	}
	assertFileExists(t, filepath.Join(bareProjectDir, "go.mod"))
	assertFileExists(t, filepath.Join(bareProjectDir, "Taskfile.yml"))
	assertFileExists(t, filepath.Join(bareProjectDir, "deployment", "docker-compose.yaml"))
	assertFileExists(t, filepath.Join(bareProjectDir, "tests", "bruno", "bruno.json"))

	t.Log("adding service 'orders' to bare project")
	if err := handleAdd(bareProjectDir, "orders"); err != nil {
		t.Fatalf("handleAdd for service in bare project failed: %v", err)
	}
	assertFileExists(t, filepath.Join(bareProjectDir, "internal", "orders", "domain.go"))
	assertFileExists(t, filepath.Join(bareProjectDir, "cmd", "orders", "main.go"))
	assertFileExists(t, filepath.Join(bareProjectDir, "conf", "orders.toml"))
	assertFileExists(t, filepath.Join(bareProjectDir, "deployment", "orders.compose.yaml"))

	t.Log("verifying duplicate project creation failure")
	if err := initProject(bareProjectDir, "another"); err == nil {
		t.Fatal("expected initProject to fail when project directory already exists, but it succeeded")
	}

	t.Log("verifying rejection of invalid service/subdomain targets")
	badTargets := []string{
		"",
		"orders/sub/too/deep",
		"orders/invalid*name",
		"nonexistent_parent/sub",
		"../traversal",
		"/absolute/path",
	}
	for _, badTarget := range badTargets {
		if err := handleAdd(bareProjectDir, badTarget); err == nil {
			t.Errorf("expected handleAdd(%q) to fail, but it succeeded", badTarget)
		}
	}

	t.Log("verifying rejection of duplicate service and subdomain creation")
	if err := handleAdd(bareProjectDir, "orders"); err == nil {
		t.Fatal("expected handleAdd('orders') to fail for existing service, but it succeeded")
	}

	if err := handleAdd(bareProjectDir, "orders/fulfillment"); err != nil {
		t.Fatalf("first handleAdd('orders/fulfillment') failed: %v", err)
	}
	if err := handleAdd(bareProjectDir, "orders/fulfillment"); err == nil {
		t.Fatal("expected duplicate handleAdd('orders/fulfillment') to fail, but it succeeded")
	}

	t.Log("verifying rejection of invalid link target paths")
	if err := linkProject(bareProjectDir, filepath.Join(tempDir, "nonexistent_dir_12345")); err == nil {
		t.Fatal("expected linkProject to nonexistent path to fail, but it succeeded")
	}
	if err := linkProject(bareProjectDir, tempDir); err == nil {
		t.Fatal("expected linkProject to non-sling directory to fail, but it succeeded")
	}

	t.Log("verifying unlink idempotency on unlinked project")
	if err := unlinkProject(bareProjectDir); err != nil {
		t.Fatalf("unlinkProject failed on unlinked project: %v", err)
	}
}

func patchGoModReplace(t *testing.T, projectDir, slingAbsPath string) {
	t.Helper()
	goModPath := filepath.Join(projectDir, "go.mod")
	content := readFile(t, goModPath)
	content += "\nreplace github.com/blessed-go/sling => " + slingAbsPath + "\n"
	if err := os.WriteFile(goModPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to patch go.mod: %v", err)
	}
}

func runGoBuild(t *testing.T, dir, packagePath string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", os.DevNull, packagePath)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compilation failed for %s:\n%s", packagePath, string(output))
	}
}

func runGoModTidy(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go mod tidy failed:\n%s", string(output))
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to exist at %s, but got err: %v", path, err)
	}
}

func assertFileNotExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("file SHOULD NOT exist at %s, but it was found", path)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file %s: %v", path, err)
	}
	return string(bytes)
}
