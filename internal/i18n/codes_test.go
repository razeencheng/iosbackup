package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 业务代码引用生成的常量；测试保留协议字面量，以便发现对外消息码被意外修改。
func TestAPICallSitesUseGeneratedConstants(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, sourceDir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, sourceDir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || filepath.Clean(path) == filepath.Join(root, "internal", "i18n", "codes.go") {
				return nil
			}
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(value, "api.") && value != "api." {
					t.Errorf("%s: use an i18n constant instead of %q", set.Position(literal.Pos()), value)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
