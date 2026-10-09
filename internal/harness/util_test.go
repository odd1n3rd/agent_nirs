package harness

import (
	"os"
	"path/filepath"
)

func mkdirWrite(dir, skill string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skill), 0o644)
}
