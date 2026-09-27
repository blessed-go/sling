package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectLifecycleSmoke(t *testing.T) {
	// Set up isolated test directory.
	tempDir := t.TempDir()
	projectName := "smokeproj"
	projectDir := filepath.Join(tempDir, projectName)

	slingRepoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("failed to get sling repo root: %v", err)
	}

	t.Log("init project with booking service")
	if err := initProject(projectDir, "booking"); err != nil {
		t.Fatalf("initProject failed: %v", err)
	}

	t.Log("verifying generated go.mod has no hardcoded replace directives")
	rawGoMod := readFile(t, filepath.Join(projectDir, "go.mod"))
	if strings.Contains(rawGoMod, "replace") || strings.Contains(rawGoMod, "../sling") {
		t.Fatalf("generated go.mod contains hardcoded replace directives:\n%s", rawGoMod)
	}

	t.Log("testing linkProject")
	if err := linkProject(projectDir, slingRepoRoot); err != nil {
		t.Fatalf("linkProject failed: %v", err)
	}
	assertFileExists(t, filepath.Join(projectDir, "go.work"))
	goWorkContent := readFile(t, filepath.Join(projectDir, "go.work"))
	if !strings.Contains(goWorkContent, "use (") {
		t.Fatalf("invalid go.work content:\n%s", goWorkContent)
	}
	assertFileExists(t, filepath.Join(projectDir, "deployment", "docker-compose.override.yaml"))
	assertFileExists(t, filepath.Join(projectDir, "deployment", "go.work.docker"))
	overrideContent := readFile(t, filepath.Join(projectDir, "deployment", "docker-compose.override.yaml"))
	if !strings.Contains(overrideContent, "booking:") || !strings.Contains(overrideContent, "/app/sling") {
		t.Fatalf("invalid docker-compose.override.yaml:\n%s", overrideContent)
	}

	t.Log("testing linkProject with empty slingPath (preserving existing linked path)")
	if err := linkProject(projectDir, ""); err != nil {
		t.Fatalf("linkProject with empty slingPath failed: %v", err)
	}
	goWorkContent = readFile(t, filepath.Join(projectDir, "go.work"))
	if !strings.Contains(goWorkContent, "replace github.com/blessed-go/sling =>") {
		t.Fatalf("go.work lost replace directive:\n%s", goWorkContent)
	}

	t.Log("testing unlinkProject")
	if err := unlinkProject(projectDir); err != nil {
		t.Fatalf("unlinkProject failed: %v", err)
	}
	assertFileNotExists(t, filepath.Join(projectDir, "go.work"))
	assertFileNotExists(t, filepath.Join(projectDir, "deployment", "docker-compose.override.yaml"))
	assertFileNotExists(t, filepath.Join(projectDir, "deployment", "go.work.docker"))


	patchGoModReplace(t, projectDir, slingRepoRoot)


	assertFileExists(t, filepath.Join(projectDir, ".gitignore"))
	assertFileExists(t, filepath.Join(projectDir, ".dockerignore"))
	assertFileExists(t, filepath.Join(projectDir, ".env.example"))
	assertFileExists(t, filepath.Join(projectDir, "cmd", "booking", "main.go"))
	assertFileExists(t, filepath.Join(projectDir, "internal", "booking", "domain.go"))
	assertFileExists(t, filepath.Join(projectDir, "deployment", "booking.compose.yaml"))
	assertFileExists(t, filepath.Join(projectDir, "cmd", "migrate", "main.go"))

	t.Log("adding service billing")
	if err := addService(projectDir, "billing"); err != nil {
		t.Fatalf("addService(billing) failed: %v", err)
	}

	assertFileExists(t, filepath.Join(projectDir, "cmd", "billing", "main.go"))
	assertFileExists(t, filepath.Join(projectDir, "internal", "billing", "domain.go"))
	assertFileExists(t, filepath.Join(projectDir, "deployment", "billing.compose.yaml"))

	bookingMain := readFile(t, filepath.Join(projectDir, "cmd", "booking", "main.go"))
	if strings.Contains(bookingMain, "//go:build ignore") {
		t.Fatalf("generated cmd/booking/main.go still contains //go:build ignore directive")
	}
	billingMain := readFile(t, filepath.Join(projectDir, "cmd", "billing", "main.go"))
	if strings.Contains(billingMain, "//go:build ignore") {
		t.Fatalf("generated cmd/billing/main.go still contains //go:build ignore directive")
	}

	composeContent := readFile(t, filepath.Join(projectDir, "deployment", "docker-compose.yaml"))
	if !strings.Contains(composeContent, "billing.compose.yaml") {
		t.Fatalf("billing.compose.yaml not found in docker-compose.yaml include list")
	}
	if !strings.Contains(composeContent, "/var/run/docker.sock:/var/run/docker.sock:ro") {
		t.Fatalf("docker-compose.yaml does not mount docker.sock with :ro for promtail")
	}

	bookingCompose := readFile(t, filepath.Join(projectDir, "deployment", "booking.compose.yaml"))
	if !strings.Contains(bookingCompose, "${BOOKING_DELVE_PORT:-2345}:2345") {
		t.Fatalf("booking.compose.yaml does not have expected delve port 2345:\n%s", bookingCompose)
	}

	billingCompose := readFile(t, filepath.Join(projectDir, "deployment", "billing.compose.yaml"))
	if !strings.Contains(billingCompose, "${BILLING_DELVE_PORT:-2346}:2345") {
		t.Fatalf("billing.compose.yaml does not have expected delve port 2346:\n%s", billingCompose)
	}

	// Verify configs are non-empty and service-specific
	bookingConf := readFile(t, filepath.Join(projectDir, "conf", "conf.toml"))
	if !strings.Contains(bookingConf, `service_name = "booking"`) {
		t.Fatalf("conf/conf.toml does not contain service_name = booking:\n%s", bookingConf)
	}
	billingConf := readFile(t, filepath.Join(projectDir, "conf", "billing.toml"))
	if !strings.Contains(billingConf, `service_name = "billing"`) {
		t.Fatalf("conf/billing.toml does not contain service_name = billing:\n%s", billingConf)
	}

	// Verify postgres Dockerfile and init script
	assertFileExists(t, filepath.Join(projectDir, "deployment", "postgres", "Dockerfile"))
	initScript := readFile(t, filepath.Join(projectDir, "deployment", "postgres", "init-by-labels.sh"))
	if strings.Contains(initScript, "apk add") {
		t.Fatalf("init-by-labels.sh should not contain runtime apk add")
	}
	if !strings.Contains(initScript, "COMPOSE_PROJECT_NAME") {
		t.Fatalf("init-by-labels.sh should filter by COMPOSE_PROJECT_NAME")
	}

	migrateContent := readFile(t, filepath.Join(projectDir, "cmd", "migrate", "main.go"))
	if !strings.Contains(migrateContent, `"billing":`) {
		t.Fatalf("billing migration not registered in cmd/migrate/main.go")
	}

	t.Log("adding domain review")
	if err := addDomain(projectDir, "review"); err != nil {
		t.Fatalf("addDomain(review) failed: %v", err)
	}

	// Domain must exist in internal without generating cmd or compose artifacts.
	assertFileExists(t, filepath.Join(projectDir, "internal", "review", "domain.go"))
	assertFileNotExists(t, filepath.Join(projectDir, "cmd", "review", "main.go"))
	assertFileNotExists(t, filepath.Join(projectDir, "deployment", "review.compose.yaml"))

	t.Log("validating template leftovers")
	if err := validateLeftovers(projectDir); err != nil {
		t.Fatalf("unresolved placeholders found: %v", err)
	}

	t.Log("running go mod tidy")
	runGoModTidy(t, projectDir)

	t.Log("compiling generated binaries")
	runGoBuild(t, projectDir, "./cmd/booking")
	runGoBuild(t, projectDir, "./cmd/billing")
	runGoBuild(t, projectDir, "./cmd/migrate")

	t.Log("all generated binaries compiled successfully")
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

func runGoModTidy(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go mod tidy failed:\n%s", string(output))
	}
}

func TestInitWithoutServiceSmoke(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "bareproj")

	t.Log("init bare project without first service")
	if err := initProject(projectDir, ""); err != nil {
		t.Fatalf("initProject without service failed: %v", err)
	}

	assertFileExists(t, filepath.Join(projectDir, "go.mod"))
	assertFileExists(t, filepath.Join(projectDir, "Taskfile.yml"))
	assertFileExists(t, filepath.Join(projectDir, "deployment", "docker-compose.yaml"))
	assertFileExists(t, filepath.Join(projectDir, "tests", "bruno", "bruno.json"))

	t.Log("adding service later via handleAdd")
	if err := handleAdd(projectDir, "orders"); err != nil {
		t.Fatalf("handleAdd for service failed: %v", err)
	}

	assertFileExists(t, filepath.Join(projectDir, "internal", "orders", "domain.go"))
	assertFileExists(t, filepath.Join(projectDir, "cmd", "orders", "main.go"))
	assertFileExists(t, filepath.Join(projectDir, "conf", "conf.toml"))
	assertFileExists(t, filepath.Join(projectDir, "deployment", "orders.compose.yaml"))
	assertFileExists(t, filepath.Join(projectDir, "tests", "bruno", "orders", "1_create_item.bru"))

	assertFileExists(t, filepath.Join(projectDir, "conf", "orders.toml"))
	ordersCompose := readFile(t, filepath.Join(projectDir, "deployment", "orders.compose.yaml"))
	if !strings.Contains(ordersCompose, "CONFIG_PATH=conf/orders.toml") {
		t.Fatalf("orders.compose.yaml does not contain CONFIG_PATH=conf/orders.toml:\n%s", ordersCompose)
	}

	t.Log("adding subdomain via handleAdd with slash notation")
	if err := handleAdd(projectDir, "orders/analytics"); err != nil {
		t.Fatalf("handleAdd for subdomain failed: %v", err)
	}
	assertFileExists(t, filepath.Join(projectDir, "internal", "orders", "analytics", "domain.go"))

	migrateContent := readFile(t, filepath.Join(projectDir, "cmd", "migrate", "main.go"))
	if !strings.Contains(migrateContent, `orders_analytics "bareproj/internal/orders/analytics"`) {
		t.Fatalf("cmd/migrate/main.go does not import subdomain migration package with alias:\n%s", migrateContent)
	}
	if !strings.Contains(migrateContent, `"orders": {orders.Migration, orders_analytics.Migration}`) {
		t.Fatalf("cmd/migrate/main.go does not register subdomain migration in orders slice:\n%s", migrateContent)
	}

	t.Log("adding another service and identical subdomain name (analytics) to test package collision prevention")
	if err := handleAdd(projectDir, "billing"); err != nil {
		t.Fatalf("handleAdd for billing service failed: %v", err)
	}
	if err := handleAdd(projectDir, "billing/analytics"); err != nil {
		t.Fatalf("handleAdd for billing/analytics subdomain failed: %v", err)
	}
	migrateContent = readFile(t, filepath.Join(projectDir, "cmd", "migrate", "main.go"))
	if !strings.Contains(migrateContent, `orders_analytics "bareproj/internal/orders/analytics"`) {
		t.Fatalf("missing orders_analytics alias in migrate/main.go:\n%s", migrateContent)
	}
	if !strings.Contains(migrateContent, `billing_analytics "bareproj/internal/billing/analytics"`) {
		t.Fatalf("missing billing_analytics alias in migrate/main.go:\n%s", migrateContent)
	}
	if !strings.Contains(migrateContent, `"orders": {orders.Migration, orders_analytics.Migration}`) {
		t.Fatalf("missing orders_analytics migration registration:\n%s", migrateContent)
	}
	if !strings.Contains(migrateContent, `"billing": {billing.Migration, billing_analytics.Migration}`) {
		t.Fatalf("missing billing_analytics migration registration:\n%s", migrateContent)
	}

	ordersAnalyticsRepo := readFile(t, filepath.Join(projectDir, "internal", "orders", "analytics", "repo_cached.go"))
	if !strings.Contains(ordersAnalyticsRepo, `"orders:analytics:items:%s"`) {
		t.Fatalf("orders/analytics repo_cached.go missing scoped redis key prefix:\n%s", ordersAnalyticsRepo)
	}
	billingAnalyticsRepo := readFile(t, filepath.Join(projectDir, "internal", "billing", "analytics", "repo_cached.go"))
	if !strings.Contains(billingAnalyticsRepo, `"billing:analytics:items:%s"`) {
		t.Fatalf("billing/analytics repo_cached.go missing scoped redis key prefix:\n%s", billingAnalyticsRepo)
	}

	ordersAnalyticsSQL := readFile(t, filepath.Join(projectDir, "internal", "orders", "analytics", "migrations", "20260101000000_init.sql"))
	if !strings.Contains(ordersAnalyticsSQL, "CREATE TABLE IF NOT EXISTS analytics_items") {
		t.Fatalf("orders/analytics init.sql missing isolated table name:\n%s", ordersAnalyticsSQL)
	}
	if !strings.Contains(ordersAnalyticsSQL, "idx_analytics_items_created_at ON analytics_items") {
		t.Fatalf("orders/analytics init.sql missing isolated index name:\n%s", ordersAnalyticsSQL)
	}

	ordersAnalyticsPostgres := readFile(t, filepath.Join(projectDir, "internal", "orders", "analytics", "repo_postgres.go"))
	if !strings.Contains(ordersAnalyticsPostgres, "FROM analytics_items") || !strings.Contains(ordersAnalyticsPostgres, "INTO analytics_items") {
		t.Fatalf("orders/analytics repo_postgres.go does not query analytics_items:\n%s", ordersAnalyticsPostgres)
	}

	t.Log("testing invalid names and nonexistent parent")
	if err := handleAdd(projectDir, "nonexistent/sub"); err == nil {
		t.Fatal("expected error adding subdomain to nonexistent service, got nil")
	}
	if err := handleAdd(projectDir, "invalid name!"); err == nil {
		t.Fatal("expected error with invalid name characters, got nil")
	}
}

func TestVersionCommand(t *testing.T) {
	if version != "v0.1.0" {
		t.Errorf("expected version to be 'v0.1.0', got %q", version)
	}
}


