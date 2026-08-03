package cli_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The CLI is public-API evidence: it must not reach past the Connect surface
// into store, queue, or loop packages.
func TestCLIUsesOnlyPublicAPIs(t *testing.T) {
	forbidden := []string{
		"github.com/aleksclark/ultracore/postgres",
		"github.com/aleksclark/ultracore/jobqueue",
		"github.com/aleksclark/ultracore/loop",
		"github.com/aleksclark/ultracore/envwork",
		"github.com/jackc/pgx/v5",
	}
	root := filepath.Join("..", "..", "..", "cmd", "core")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, imported := range file.Imports {
			value := strings.Trim(imported.Path.Value, `"`)
			for _, banned := range forbidden {
				if value == banned || strings.HasPrefix(value, banned+"/") {
					t.Errorf("%s imports %s; the CLI must go through the public API", path, value)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
