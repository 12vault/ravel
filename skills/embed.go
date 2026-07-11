package skills

import (
	"embed"
	"io/fs"
	"path"
)

// Ravel is the agent skill shipped with the CLI.
//
//go:embed ravel/skill.md
var Ravel []byte

//go:embed ravel/references/*.md
var bundle embed.FS

func ReferenceFiles() (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(bundle, "ravel/references", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := bundle.ReadFile(name)
		if err != nil {
			return err
		}
		files[path.Base(name)] = data
		return nil
	})
	return files, err
}
