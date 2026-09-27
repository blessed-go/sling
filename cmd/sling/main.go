package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/blessed-go/sling/template"
)

const baseModulePath = "github.com/blessed-go/sling/template"

var leftoverRegex = regexp.MustCompile(`__[A-Z0-9_]+__`)

const version = "v0.1.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "version", "--version", "-v":
		fmt.Printf("sling %s\n", version)
		return

	case "init":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: sling init <project-name> [first-service]")
			os.Exit(1)
		}
		projectName := os.Args[2]
		firstService := ""
		if len(os.Args) >= 4 {
			firstService = strings.ToLower(os.Args[3])
		}

		if err := initProject(projectName, firstService); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "add", "new-service":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: sling add <service> or sling add <service>/<subdomain>")
			os.Exit(1)
		}
		target := strings.ToLower(os.Args[2])
		if err := handleAdd(".", target); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "new-domain":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: sling add <service>/<domain> (or sling new-domain <service> <domain>)")
			os.Exit(1)
		}
		target := strings.ToLower(os.Args[2])
		if len(os.Args) >= 4 {
			target = target + "/" + strings.ToLower(os.Args[3])
		}
		if err := handleAdd(".", target); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "link":
		slingPath := "../sling"
		if len(os.Args) >= 3 {
			slingPath = os.Args[2]
		}
		if err := linkProject(".", slingPath); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "unlink":
		if err := unlinkProject("."); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Sling CLI — Developer Tool")
	fmt.Println("\nCommands:")
	fmt.Println("  init  <project-name> [first-service]  Create a new project workspace")
	fmt.Println("  add   <service>                       Add a deployable microservice (e.g. sling add billing)")
	fmt.Println("  add   <service>/<domain>              Add a domain inside a service (e.g. sling add billing/analytics)")
	fmt.Println("  version                               Show Sling version")
	fmt.Println("  link  [path-to-sling]                 Link local Sling repository for development (default: ../sling)")
	fmt.Println("  unlink                                Unlink local Sling and restore standalone configuration")
}

func getTemplateFS() (fs.FS, error) {
	if envPath := os.Getenv("SLING_TEMPLATE_DIR"); envPath != "" {
		if info, err := os.Stat(envPath); err == nil && info.IsDir() {
			return os.DirFS(envPath), nil
		}
		return nil, fmt.Errorf("SLING_TEMPLATE_DIR set to %q, but directory not found", envPath)
	}
	return template.FS, nil
}

var (
	validModuleNameRegex = regexp.MustCompile(`^[a-z0-9_.-]+$`)
	validIdentifierRegex = regexp.MustCompile(`^[a-z0-9_]+$`)
)

func cleanTemplateContent(content []byte) string {
	text := string(content)
	text = strings.TrimPrefix(text, "//go:build ignore\n\n")
	text = strings.TrimPrefix(text, "//go:build ignore\r\n\r\n")
	return text
}

func validateName(name, entityType string) error {
	if entityType == "project" {
		if !validModuleNameRegex.MatchString(name) {
			return fmt.Errorf("invalid project name %q: must contain only lowercase alphanumeric characters, dashes, dots, or underscores", name)
		}
		return nil
	}

	if !validIdentifierRegex.MatchString(name) {
		return fmt.Errorf("invalid %s name %q: must contain only lowercase alphanumeric characters and underscores (e.g. %s_core) to be a valid Go package", entityType, name, entityType)
	}
	return nil
}

func handleAdd(projectDir, target string) error {
	if strings.Contains(target, "/") {
		parts := strings.Split(target, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("invalid target format %q: expected <service>/<subdomain> (e.g. billing/analytics)", target)
		}
		serviceName, domainName := parts[0], parts[1]
		if err := validateName(serviceName, "service"); err != nil {
			return err
		}
		if err := validateName(domainName, "domain"); err != nil {
			return err
		}
		return addSubdomain(projectDir, serviceName, domainName)
	}

	if err := validateName(target, "service"); err != nil {
		return err
	}
	return addService(projectDir, target)
}

func addSubdomain(projectDir, serviceName, domainName string) error {
	parentDir := filepath.Join(projectDir, "internal", serviceName)
	if _, err := os.Stat(parentDir); os.IsNotExist(err) {
		return fmt.Errorf("parent service %q does not exist (internal/%s not found); create service first with 'sling add %s'", serviceName, serviceName, serviceName)
	}

	subdomainDir := filepath.Join(parentDir, domainName)
	if _, err := os.Stat(subdomainDir); err == nil {
		return fmt.Errorf("domain %s/%s already exists at internal/%s/%s", serviceName, domainName, serviceName, domainName)
	}

	moduleName, err := getModuleName(projectDir)
	if err != nil {
		return err
	}

	tfs, err := getTemplateFS()
	if err != nil {
		return err
	}

	fmt.Printf("scaffolding domain [%s] inside service [%s]...\n", domainName, serviceName)

	const srcDomain = "service/internal/domain"
	err = fs.WalkDir(tfs, srcDomain, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(srcDomain, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(subdomainDir, relPath)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		content, err := fs.ReadFile(tfs, path)
		if err != nil {
			return err
		}

		text := cleanTemplateContent(content)
		text = strings.ReplaceAll(text, "package domain", "package "+domainName)
		text = strings.ReplaceAll(text, baseModulePath+"/internal/domain", moduleName+"/internal/"+serviceName+"/"+domainName)
		keyPrefix := fmt.Sprintf("%s:%s", serviceName, domainName)
		text = strings.ReplaceAll(text, "__SERVICE__:items:", keyPrefix+":items:")
		text = strings.ReplaceAll(text, "__SERVICE__:", keyPrefix+":")
		tableName := domainName + "_items"
		text = strings.ReplaceAll(text, "TABLE IF NOT EXISTS items", "TABLE IF NOT EXISTS "+tableName)
		text = strings.ReplaceAll(text, "TABLE IF EXISTS items", "TABLE IF EXISTS "+tableName)
		text = strings.ReplaceAll(text, "idx_items_created_at ON items", "idx_"+tableName+"_created_at ON "+tableName)
		text = strings.ReplaceAll(text, "FROM items", "FROM "+tableName)
		text = strings.ReplaceAll(text, "INTO items", "INTO "+tableName)
		text = strings.ReplaceAll(text, "__SERVICE__", domainName)
		text = strings.ReplaceAll(text, "__PROJECT__", moduleName)

		mode := fs.FileMode(0644)
		if strings.HasSuffix(destPath, ".sh") {
			mode = 0755
		}

		return os.WriteFile(destPath, []byte(text), mode)
	})
	if err != nil {
		return err
	}

	if err := validateLeftovers(subdomainDir); err != nil {
		return err
	}

	migratePath := filepath.Join(projectDir, "cmd", "migrate", "main.go")
	if mContent, err := os.ReadFile(migratePath); err == nil {
		mText := string(mContent)
		alias := fmt.Sprintf("%s_%s", serviceName, domainName)
		subdomainImport := fmt.Sprintf("\t%s \"%s/internal/%s/%s\"\n\t// migrations:imports", alias, moduleName, serviceName, domainName)
		if !strings.Contains(mText, `"`+moduleName+"/internal/"+serviceName+"/"+domainName+`"`) {
			mText = strings.Replace(mText, "// migrations:imports", subdomainImport, 1)
		}

		targetKey := fmt.Sprintf(`"%s": {`, serviceName)
		if idx := strings.Index(mText, targetKey); idx != -1 {
			rest := mText[idx:]
			if closeIdx := strings.Index(rest, "}"); closeIdx != -1 {
				beforeClose := mText[:idx+closeIdx]
				afterClose := mText[idx+closeIdx:]
				migrationRef := alias + ".Migration"
				if !strings.Contains(beforeClose[idx:], migrationRef) {
					trimmedBefore := strings.TrimRight(beforeClose, " \t\r\n")
					if strings.HasSuffix(trimmedBefore, ",") {
						mText = trimmedBefore + " " + migrationRef + afterClose
					} else {
						mText = trimmedBefore + ", " + migrationRef + afterClose
					}
					_ = os.WriteFile(migratePath, []byte(mText), 0644)
					fmt.Printf("   auto-registered migration [%s.Migration] for service [%s] in %s\n", alias, serviceName, migratePath)
				}
			}
		}
	}

	fmt.Printf("domain [%s] ready at internal/%s/%s\n\n", domainName, serviceName, domainName)
	return nil
}

func addDomain(projectDir, domainName string) error {
	moduleName, err := getModuleName(projectDir)
	if err != nil {
		return err
	}

	tfs, err := getTemplateFS()
	if err != nil {
		return err
	}

	domainDir := filepath.Join(projectDir, "internal", domainName)
	if _, err := os.Stat(domainDir); err == nil {
		return fmt.Errorf("domain internal/%s already exists", domainName)
	}

	fmt.Printf("scaffolding domain [%s] in module [%s]...\n", domainName, moduleName)

	const srcDomain = "service/internal/domain"
	err = fs.WalkDir(tfs, srcDomain, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(srcDomain, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(domainDir, relPath)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		content, err := fs.ReadFile(tfs, path)
		if err != nil {
			return err
		}

		text := cleanTemplateContent(content)
		text = strings.ReplaceAll(text, "package domain", "package "+domainName)
		text = strings.ReplaceAll(text, baseModulePath+"/internal/domain", moduleName+"/internal/"+domainName)
		text = strings.ReplaceAll(text, "__SERVICE__", domainName)
		text = strings.ReplaceAll(text, "__PROJECT__", moduleName)

		mode := fs.FileMode(0644)
		if strings.HasSuffix(destPath, ".sh") {
			mode = 0755
		}

		return os.WriteFile(destPath, []byte(text), mode)
	})
	if err != nil {
		return err
	}

	if err := validateLeftovers(domainDir); err != nil {
		return err
	}

	fmt.Printf("domain [%s] ready at internal/%s\n", domainName, domainName)
	return nil
}

func addService(projectDir, serviceName string) error {
	if err := addDomain(projectDir, serviceName); err != nil {
		return err
	}

	moduleName, err := getModuleName(projectDir)
	if err != nil {
		return err
	}
	tfs, err := getTemplateFS()
	if err != nil {
		return err
	}

	cmdDir := filepath.Join(projectDir, "cmd", serviceName)
	fmt.Printf("wiring service harness for [%s]...\n", serviceName)

	if err := os.MkdirAll(cmdDir, 0755); err != nil {
		return err
	}

	if cmdContent, err := fs.ReadFile(tfs, "service/cmd/main.go"); err == nil {
		text := cleanTemplateContent(cmdContent)
		text = strings.ReplaceAll(text, "__MODULE__", moduleName)
		text = strings.ReplaceAll(text, "__SERVICE__", serviceName)
		_ = os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte(text), 0644)
	}

	if fragContent, err := fs.ReadFile(tfs, "service/compose.fragment.yaml"); err == nil {
		fragText := string(fragContent)

		existingFragments, _ := filepath.Glob(filepath.Join(projectDir, "deployment", "*.compose.yaml"))
		delvePort := 2345 + len(existingFragments)
		serviceUpper := strings.ToUpper(strings.ReplaceAll(serviceName, "-", "_"))

		fragText = strings.ReplaceAll(fragText, "__SERVICE_UPPER__", serviceUpper)
		fragText = strings.ReplaceAll(fragText, "__DELVE_PORT__", strconv.Itoa(delvePort))
		fragText = strings.ReplaceAll(fragText, "__SERVICE__", serviceName)
		fragText = strings.ReplaceAll(fragText, "__PROJECT__", moduleName)

		destCompose := filepath.Join(projectDir, "deployment", serviceName+".compose.yaml")
		_ = os.WriteFile(destCompose, []byte("services:\n"+fragText), 0644)

		composePath := filepath.Join(projectDir, "deployment", "docker-compose.yaml")
		if composeContent, err := os.ReadFile(composePath); err == nil {
			text := string(composeContent)
			includeLine := fmt.Sprintf("  - %s.compose.yaml\n", serviceName)
			if strings.Contains(text, "include:\n") {
				text = strings.Replace(text, "include:\n", "include:\n"+includeLine, 1)
			} else {
				text = strings.Replace(text, "name: "+moduleName+"\n", "name: "+moduleName+"\n\ninclude:\n"+includeLine, 1)
			}
			_ = os.WriteFile(composePath, []byte(text), 0644)
			fmt.Printf("   registered in %s (include)\n", composePath)
		}
	}

	migratePath := filepath.Join(projectDir, "cmd", "migrate", "main.go")
	if mContent, err := os.ReadFile(migratePath); err == nil {
		mText := string(mContent)
		if !strings.Contains(mText, `"`+serviceName+`":`) {
			importLine := fmt.Sprintf("\t\"%s/internal/%s\"\n\t// migrations:imports", moduleName, serviceName)
			regLine := fmt.Sprintf("\t\"%s\": {%s.Migration},\n\t// migrations:registry", serviceName, serviceName)

			mText = strings.Replace(mText, "// migrations:imports", importLine, 1)
			mText = strings.Replace(mText, "// migrations:registry", regLine, 1)

			_ = os.WriteFile(migratePath, []byte(mText), 0644)
			fmt.Printf("   auto-registered migration in %s\n", migratePath)
		}
	}

	confDir := filepath.Join(projectDir, "conf")
	_ = os.MkdirAll(confDir, 0755)
	if confContent, err := fs.ReadFile(tfs, "service/conf/conf.toml"); err == nil {
		text := string(confContent)
		text = strings.ReplaceAll(text, "__SERVICE__", serviceName)
		text = strings.ReplaceAll(text, "__PROJECT__", moduleName)

		serviceConf := filepath.Join(confDir, serviceName+".toml")
		_ = os.WriteFile(serviceConf, []byte(text), 0644)

		rootConf := filepath.Join(confDir, "conf.toml")
		if _, err := os.Stat(rootConf); err != nil {
			_ = os.WriteFile(rootConf, []byte(text), 0644)
		}
	}

	brunoDir := filepath.Join(projectDir, "tests", "bruno", serviceName)
	_ = os.MkdirAll(brunoDir, 0755)
	const srcBruno = "service/tests/bruno"
	_ = fs.WalkDir(tfs, srcBruno, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		content, err := fs.ReadFile(tfs, path)
		if err != nil {
			return nil
		}
		text := string(content)
		text = strings.ReplaceAll(text, "__SERVICE__", serviceName)
		text = strings.ReplaceAll(text, "__PROJECT__", moduleName)

		relFile, _ := filepath.Rel(srcBruno, path)
		_ = os.WriteFile(filepath.Join(brunoDir, relFile), []byte(text), 0644)
		return nil
	})

	if err := validateLeftovers(cmdDir); err != nil {
		return err
	}

	// If local Sling is linked, update deployment/docker-compose.override.yaml with the new service
	overridePath := filepath.Join(projectDir, "deployment", "docker-compose.override.yaml")
	if _, err := os.Stat(overridePath); err == nil {
		_ = linkProject(projectDir, "")
	}

	fmt.Printf("service [%s] added\n\n", serviceName)
	return nil
}

func getModuleName(dir string) (string, error) {
	goModPath := filepath.Join(dir, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return "", fmt.Errorf("could not read go.mod: are you in a project root? %w", err)
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", fmt.Errorf("no module statement found in go.mod")
}

func initProject(targetDir, firstService string) error {
	if _, err := os.Stat(targetDir); err == nil {
		return fmt.Errorf("directory %s already exists", targetDir)
	}

	moduleName := strings.ToLower(filepath.Base(targetDir))
	if err := validateName(moduleName, "project"); err != nil {
		return err
	}
	if firstService != "" {
		if err := validateName(firstService, "service"); err != nil {
			return err
		}
	}

	tfs, err := getTemplateFS()
	if err != nil {
		return fmt.Errorf("find template dir: %w", err)
	}

	fmt.Printf("initializing project [%s] at [%s]...\n", moduleName, targetDir)

	replacements := []string{
		"__PROJECT__", moduleName,
		"__MODULE__", moduleName,
	}
	if firstService != "" {
		replacements = append(replacements, "__SERVICE__", firstService)
	}
	replacer := strings.NewReplacer(replacements...)

	const srcProject = "project"
	err = fs.WalkDir(tfs, srcProject, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(srcProject, path)
		if err != nil {
			return err
		}
		relPath = replacer.Replace(relPath)
		destPath := filepath.Join(targetDir, relPath)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		content, err := fs.ReadFile(tfs, path)
		if err != nil {
			return err
		}

		text := cleanTemplateContent(content)
		text = replacer.Replace(text)

		mode := fs.FileMode(0644)
		if strings.HasSuffix(destPath, ".sh") {
			mode = 0755
		}

		return os.WriteFile(destPath, []byte(text), mode)
	})
	if err != nil {
		return fmt.Errorf("copying template files: %w", err)
	}

	goModContent := fmt.Sprintf(`module %s

go 1.27

require (
	github.com/blessed-go/sling v0.1.0
	github.com/go-chi/chi/v5 v5.3.2
	github.com/jackc/pgx/v5 v5.11.0
)
`, moduleName)

	if err := os.WriteFile(filepath.Join(targetDir, "go.mod"), []byte(goModContent), 0644); err != nil {
		return fmt.Errorf("writing go.mod: %w", err)
	}

	fmt.Printf("project [%s] initialized\n", moduleName)

	if firstService != "" {
		fmt.Printf("scaffolding service [%s]...\n", firstService)
		if err := addService(targetDir, firstService); err != nil {
			return fmt.Errorf("scaffolding service [%s]: %w", firstService, err)
		}
	}

	if err := validateLeftovers(targetDir); err != nil {
		return fmt.Errorf("validation leftovers: %w", err)
	}

	fmt.Println("\nnext steps:")
	fmt.Printf("   1. cd %s\n", targetDir)
	fmt.Println("   2. go mod tidy")
	fmt.Println("   3. task up")

	return nil
}

func extractLinkedSlingPath(projectDir string) string {
	goWorkPath := filepath.Join(projectDir, "go.work")
	if data, err := os.ReadFile(goWorkPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			const prefix = "replace github.com/blessed-go/sling =>"
			if strings.HasPrefix(line, prefix) {
				target := strings.TrimSpace(strings.TrimPrefix(line, prefix))
				target = strings.Trim(target, "\"'")
				if target != "" {
					return target
				}
			}
		}
	}

	overridePath := filepath.Join(projectDir, "deployment", "docker-compose.override.yaml")
	if data, err := os.ReadFile(overridePath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "- ") && strings.HasSuffix(line, ":/app/sling") {
				raw := strings.TrimPrefix(line, "- ")
				raw = strings.TrimSuffix(raw, ":/app/sling")
				raw = strings.Trim(strings.TrimSpace(raw), "\"'")
				if raw != "" {
					return filepath.Join("deployment", raw)
				}
			}
		}
	}

	return ""
}

func linkProject(projectDir, slingPath string) error {
	if slingPath == "" {
		if existing := extractLinkedSlingPath(projectDir); existing != "" {
			slingPath = existing
		} else {
			slingPath = "../sling"
		}
	}

	if !filepath.IsAbs(slingPath) {
		slingPath = filepath.Join(projectDir, slingPath)
	}
	absSling, err := filepath.Abs(slingPath)
	if err != nil {
		return fmt.Errorf("resolving sling path %q: %w", slingPath, err)
	}

	slingGoMod := filepath.Join(absSling, "go.mod")
	modData, err := os.ReadFile(slingGoMod)
	if err != nil {
		return fmt.Errorf("cannot read %s: verify sling repository path: %w", slingGoMod, err)
	}
	if !strings.Contains(string(modData), "module github.com/blessed-go/sling") {
		return fmt.Errorf("%s is not the github.com/blessed-go/sling module", slingGoMod)
	}

	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return fmt.Errorf("resolving project directory %q: %w", projectDir, err)
	}

	moduleName, err := getModuleName(absProjectDir)
	if err != nil {
		return err
	}

	relToSling, err := filepath.Rel(absProjectDir, absSling)
	if err != nil {
		return fmt.Errorf("calculating relative path: %w", err)
	}
	relToSlingSlash := filepath.ToSlash(relToSling)

	goWorkContent := fmt.Sprintf(`go 1.27

use (
	.
)

replace github.com/blessed-go/sling => %s
`, relToSlingSlash)

	goWorkPath := filepath.Join(absProjectDir, "go.work")
	if err := os.WriteFile(goWorkPath, []byte(goWorkContent), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", goWorkPath, err)
	}

	deploymentDir := filepath.Join(absProjectDir, "deployment")
	var overriddenServices []string
	if info, err := os.Stat(deploymentDir); err == nil && info.IsDir() {
		relFromDeploy, err := filepath.Rel(deploymentDir, absSling)
		if err != nil {
			return fmt.Errorf("calculating relative path from deployment: %w", err)
		}
		relFromDeploySlash := filepath.ToSlash(relFromDeploy)

		entries, err := os.ReadDir(deploymentDir)
		if err == nil {
			for _, entry := range entries {
				name := entry.Name()
				if !entry.IsDir() && strings.HasSuffix(name, ".compose.yaml") {
					if name == "docker-compose.yaml" || name == "docker-compose.override.yaml" {
						continue
					}
					svcName := strings.TrimSuffix(name, ".compose.yaml")
					overriddenServices = append(overriddenServices, svcName, svcName+"-migrator")
				}
			}
		}

		if len(overriddenServices) > 0 {
			dockerGoWork := fmt.Sprintf(`go 1.27

use (
	/app/%s
)

replace github.com/blessed-go/sling => /app/sling
`, moduleName)
			dockerGoWorkPath := filepath.Join(deploymentDir, "go.work.docker")
			if err := os.WriteFile(dockerGoWorkPath, []byte(dockerGoWork), 0644); err != nil {
				return fmt.Errorf("writing %s: %w", dockerGoWorkPath, err)
			}

			var b strings.Builder
			b.WriteString("# Code generated by 'sling link'. Local platform development override. DO NOT COMMIT.\n")
			b.WriteString("services:\n")
			for _, svc := range overriddenServices {
				b.WriteString(fmt.Sprintf("  %s:\n", svc))
				b.WriteString("    environment:\n")
				b.WriteString("      - GOWORK=/app/go.work\n")
				b.WriteString("    volumes:\n")
				b.WriteString(fmt.Sprintf("      - %s:/app/sling\n", relFromDeploySlash))
				b.WriteString("      - ./go.work.docker:/app/go.work:ro\n")
			}
			overridePath := filepath.Join(deploymentDir, "docker-compose.override.yaml")
			if err := os.WriteFile(overridePath, []byte(b.String()), 0644); err != nil {
				return fmt.Errorf("writing %s: %w", overridePath, err)
			}
		}
	}

	fmt.Printf("linked local Sling platform [%s] to project [%s]:\n", absSling, moduleName)
	fmt.Printf("  + created %s (use . %s)\n", filepath.Join(absProjectDir, "go.work"), relToSlingSlash)
	if len(overriddenServices) > 0 {
		fmt.Printf("  + created deployment/docker-compose.override.yaml and go.work.docker (services: %s)\n", strings.Join(overriddenServices, ", "))
	}
	return nil
}

func unlinkProject(projectDir string) error {
	removedCount := 0

	goWorkPath := filepath.Join(projectDir, "go.work")
	if err := os.Remove(goWorkPath); err == nil {
		fmt.Printf("  - removed %s\n", goWorkPath)
		removedCount++
	}

	goWorkSumPath := filepath.Join(projectDir, "go.work.sum")
	if err := os.Remove(goWorkSumPath); err == nil {
		fmt.Printf("  - removed %s\n", goWorkSumPath)
		removedCount++
	}

	dockerGoWorkPath := filepath.Join(projectDir, "deployment", "go.work.docker")
	if err := os.Remove(dockerGoWorkPath); err == nil {
		fmt.Printf("  - removed %s\n", dockerGoWorkPath)
		removedCount++
	}

	overridePath := filepath.Join(projectDir, "deployment", "docker-compose.override.yaml")
	if err := os.Remove(overridePath); err == nil {
		fmt.Printf("  - removed %s\n", overridePath)
		removedCount++
	}

	if removedCount == 0 {
		fmt.Println("no local Sling links found (project is already using published Sling module)")
	} else {
		fmt.Println("unlinked local Sling platform (restored clean standalone project configuration)")
	}
	return nil
}

func validateLeftovers(dir string) error {
	var found []string
	_ = filepath.Walk(dir, func(path string, info fs.FileInfo, err error) error {
		if info.IsDir() || strings.Contains(path, ".git") {
			return nil
		}
		content, _ := os.ReadFile(path)
		if matches := leftoverRegex.FindAll(content, -1); len(matches) > 0 {
			for _, m := range matches {
				found = append(found, fmt.Sprintf("%s in %s", string(m), path))
			}
		}
		return nil
	})

	if len(found) > 0 {
		return fmt.Errorf("unresolved placeholders found:\n  %s", strings.Join(found, "\n  "))
	}
	return nil
}
