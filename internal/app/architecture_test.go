package app

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type listedPackage struct {
	ImportPath string
	Imports    []string
	Module     *listedModule
	Incomplete bool
}

type listedModule struct {
	Path string
}

func TestAppPackageExportsOnlyRun(t *testing.T) {
	appDir := filepath.Join(findModuleRoot(t), "internal", "app")
	packages, err := parser.ParseDir(token.NewFileSet(), appDir, func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}

	pkg, ok := packages["app"]
	if !ok {
		t.Fatal("package app not found")
	}
	var exported []string
	for _, file := range pkg.Files {
		for _, declaration := range file.Decls {
			switch declaration := declaration.(type) {
			case *ast.FuncDecl:
				if declaration.Recv == nil && ast.IsExported(declaration.Name.Name) {
					exported = append(exported, declaration.Name.Name)
				}
			case *ast.GenDecl:
				for _, spec := range declaration.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						if ast.IsExported(spec.Name.Name) {
							exported = append(exported, spec.Name.Name)
						}
					case *ast.ValueSpec:
						for _, name := range spec.Names {
							if ast.IsExported(name.Name) {
								exported = append(exported, name.Name)
							}
						}
					}
				}
			}
		}
	}
	sort.Strings(exported)
	if got, want := strings.Join(exported, ","), "Run"; got != want {
		t.Fatalf("internal/app exported package symbols = %s, want %s", got, want)
	}
}

func TestPackageDependencyDirection(t *testing.T) {
	root := findModuleRoot(t)
	modulePath, err := loadModulePath(root)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "list", "-json", "./cmd/...", "./internal/...")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list package graph: %v: %s", err, output)
	}

	decoder := json.NewDecoder(strings.NewReader(string(output)))
	var packages []listedPackage
	for {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode go list output: %v", err)
		}
		packages = append(packages, pkg)
	}

	if err := validatePackageDependencyDirection(packages, modulePath); err != nil {
		t.Fatal(err)
	}
}

func TestPackageDependencyDirectionRejectsCmdToLeaf(t *testing.T) {
	packages := validPackageGraph("iosbackup")
	packages[0].Imports = append(packages[0].Imports, "iosbackup/internal/config")
	err := validatePackageDependencyDirection(packages, "iosbackup")
	if err == nil || !strings.Contains(err.Error(), "must depend locally only") {
		t.Fatal("cmd -> leaf dependency was accepted")
	}
}

func TestPackageDependencyDirectionRejectsLeafToApp(t *testing.T) {
	packages := append(validPackageGraph("iosbackup"), listedPackage{
		ImportPath: "iosbackup/internal/config",
		Imports:    []string{"iosbackup/internal/app"},
		Module:     &listedModule{Path: "iosbackup"},
	})
	err := validatePackageDependencyDirection(packages, "iosbackup")
	if err == nil || !strings.Contains(err.Error(), "leaf package") {
		t.Fatal("leaf -> app dependency was accepted")
	}
}

func TestPackageDependencyDirectionRejectsMismatchedModulePrefix(t *testing.T) {
	packages := []listedPackage{
		{
			ImportPath: "iosbackup/cmd/iosbackup",
			Imports:    []string{"iosbackup/internal/app"},
			Module:     &listedModule{Path: "iosbackup"},
		},
		{
			ImportPath: "iosbackup/internal/app",
			Module:     &listedModule{Path: "iosbackup"},
		},
	}
	if err := validatePackageDependencyDirection(packages, "github.com/razeencheng/iosbackup"); err == nil {
		t.Fatal("module prefix mismatch was accepted")
	}
}

func TestPackageDependencyDirectionRejectsEmptyGraph(t *testing.T) {
	if err := validatePackageDependencyDirection(nil, "iosbackup"); err == nil {
		t.Fatal("empty package graph was accepted")
	}
}

func TestPackageDependencyDirectionRejectsMissingTargets(t *testing.T) {
	packages := []listedPackage{{
		ImportPath: "iosbackup/internal/config",
		Module:     &listedModule{Path: "iosbackup"},
	}}
	if err := validatePackageDependencyDirection(packages, "iosbackup"); err == nil {
		t.Fatal("package graph without command or app targets was accepted")
	}
}

func TestPackageDependencyDirectionRejectsMissingModuleMetadata(t *testing.T) {
	packages := []listedPackage{
		{ImportPath: "iosbackup/cmd/iosbackup", Imports: []string{"iosbackup/internal/app"}},
		{ImportPath: "iosbackup/internal/app"},
	}
	if err := validatePackageDependencyDirection(packages, "iosbackup"); err == nil {
		t.Fatal("target packages without module metadata were accepted")
	}
}

func TestPackageDependencyDirectionRejectsIncompleteTarget(t *testing.T) {
	packages := validPackageGraph("iosbackup")
	packages[0].Incomplete = true
	if err := validatePackageDependencyDirection(packages, "iosbackup"); err == nil {
		t.Fatal("incomplete target package was accepted")
	}
}

func TestPackageDependencyDirectionUsesExactModuleBoundary(t *testing.T) {
	packages := validPackageGraph("foo")
	packages[0].Imports = append(packages[0].Imports, "foobar/internal/config")
	if err := validatePackageDependencyDirection(packages, "foo"); err != nil {
		t.Fatalf("neighboring module prefix was treated as local: %v", err)
	}
}

func validPackageGraph(module string) []listedPackage {
	return []listedPackage{
		{
			ImportPath: module + "/cmd/iosbackup",
			Imports:    []string{module + "/internal/app"},
			Module:     &listedModule{Path: module},
		},
		{
			ImportPath: module + "/internal/app",
			Module:     &listedModule{Path: module},
		},
	}
}

func loadModulePath(root string) (string, error) {
	cmd := exec.Command("go", "list", "-m", "-json")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go list module metadata: %w: %s", err, output)
	}
	if len(strings.TrimSpace(string(output))) == 0 {
		return "", fmt.Errorf("go list module metadata returned empty output")
	}
	var module listedModule
	if err := json.Unmarshal(output, &module); err != nil {
		return "", fmt.Errorf("decode module metadata: %w", err)
	}
	module.Path = strings.TrimSpace(module.Path)
	if module.Path == "" {
		return "", fmt.Errorf("module metadata path is empty")
	}
	return module.Path, nil
}

func validatePackageDependencyDirection(packages []listedPackage, module string) error {
	module = strings.TrimSpace(module)
	if module == "" {
		return fmt.Errorf("module path is empty")
	}
	if len(packages) == 0 {
		return fmt.Errorf("package graph is empty")
	}

	cmdPrefix := module + "/cmd/"
	internalPrefix := module + "/internal/"
	cmdPath := module + "/cmd/iosbackup"
	appPath := module + "/internal/app"
	foundCmd := false
	foundApp := false
	seen := make(map[string]struct{}, len(packages))

	for _, pkg := range packages {
		pkg.ImportPath = strings.TrimSpace(pkg.ImportPath)
		if pkg.ImportPath == "" {
			return fmt.Errorf("package graph contains an empty import path")
		}
		if pkg.Incomplete {
			return fmt.Errorf("package graph contains incomplete package %s", pkg.ImportPath)
		}
		if _, exists := seen[pkg.ImportPath]; exists {
			return fmt.Errorf("package graph contains duplicate package %s", pkg.ImportPath)
		}
		seen[pkg.ImportPath] = struct{}{}
		if !isLocalImport(pkg.ImportPath, module) {
			return fmt.Errorf("go list returned package %s outside module %s", pkg.ImportPath, module)
		}
		if pkg.Module == nil || strings.TrimSpace(pkg.Module.Path) == "" {
			return fmt.Errorf("package %s has no module metadata", pkg.ImportPath)
		}
		if pkg.Module.Path != module {
			return fmt.Errorf("package %s belongs to module %s, want %s", pkg.ImportPath, pkg.Module.Path, module)
		}
		switch pkg.ImportPath {
		case cmdPath:
			foundCmd = true
		case appPath:
			foundApp = true
		}

		for _, dependency := range pkg.Imports {
			if !isLocalImport(dependency, module) {
				continue
			}
			switch {
			case strings.HasPrefix(pkg.ImportPath, cmdPrefix):
				if dependency != appPath {
					return fmt.Errorf("%s must depend locally only on %s, found %s", pkg.ImportPath, appPath, dependency)
				}
			case pkg.ImportPath == appPath:
				if strings.HasPrefix(dependency, cmdPrefix) {
					return fmt.Errorf("%s must not depend on command package %s", pkg.ImportPath, dependency)
				}
			case strings.HasPrefix(pkg.ImportPath, internalPrefix):
				if dependency == appPath || strings.HasPrefix(dependency, cmdPrefix) {
					return fmt.Errorf("leaf package %s must not depend on %s", pkg.ImportPath, dependency)
				}
			}
		}
	}
	if !foundCmd || !foundApp {
		return fmt.Errorf("package graph did not check required targets: cmd/iosbackup=%t internal/app=%t", foundCmd, foundApp)
	}
	return nil
}

func isLocalImport(importPath, module string) bool {
	return importPath == module || strings.HasPrefix(importPath, module+"/")
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
