package skills

import (
	"embed"
	"io/fs"
	"path"
	"strings"
)

// Ravel is the agent skill shipped with the CLI.
//
//go:embed ravel/skill.md
var Ravel []byte

//go:embed ravel/VERSION ravel/THIRD_PARTY_NOTICES.md ravel/references/*.md ravel/agents/* ravel/scripts/*
var bundle embed.FS

func ReferenceFiles() (map[string][]byte, error) {
	all, err := SupportFiles()
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for name, data := range all {
		if path.Dir(name) == "references" {
			files[path.Base(name)] = data
		}
	}
	return files, nil
}

func SupportFiles() (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(bundle, "ravel", func(name string, entry fs.DirEntry, walkErr error) error {
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
		relative := strings.TrimPrefix(name, "ravel/")
		files[relative] = data
		return nil
	})
	return files, err
}
