package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

func main() {
	manifest, err := findManifest()
	if err != nil {
		panic("failed to find manifest: " + err.Error())
	}

	pluginDir := filepath.Join("dist", manifest.Id)
	bundlePath := filepath.Join("dist", fmt.Sprintf("%s-%s.tar.gz", manifest.Id, manifest.Version))
	if err := packagePlugin(pluginDir, bundlePath, manifest); err != nil {
		panic("failed to package plugin bundle: " + err.Error())
	}

	fmt.Printf("plugin built at: %s\n", bundlePath)
}

func findManifest() (*model.Manifest, error) {
	_, manifestFilePath, err := model.FindManifest(".")
	if err != nil {
		return nil, errors.Wrap(err, "failed to find manifest in current working directory")
	}
	manifestFile, err := os.Open(manifestFilePath) //nolint:gosec
	if err != nil {
		return nil, errors.Wrap(err, "failed to open manifest file")
	}
	defer manifestFile.Close()

	manifest := model.Manifest{}
	if err = json.NewDecoder(manifestFile).Decode(&manifest); err != nil {
		return nil, errors.Wrap(err, "failed to decode manifest file")
	}

	return &manifest, nil
}

func packagePlugin(pluginDir, bundlePath string, manifest *model.Manifest) error {
	if err := os.MkdirAll(filepath.Dir(bundlePath), 0o755); err != nil {
		return errors.Wrap(err, "failed to create bundle directory")
	}
	if err := os.Remove(bundlePath); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "failed to remove previous bundle")
	}

	bundleFile, err := os.Create(bundlePath)
	if err != nil {
		return errors.Wrap(err, "failed to create bundle file")
	}
	defer bundleFile.Close()

	gzipWriter := gzip.NewWriter(bundleFile)
	defer gzipWriter.Close()

	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	baseDir := filepath.Dir(pluginDir)
	executablePaths := executableArchivePaths(pluginDir, manifest)
	return filepath.WalkDir(pluginDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		info, err := entry.Info()
		if err != nil {
			return errors.Wrap(err, "failed to stat bundle entry")
		}

		relativePath, err := filepath.Rel(baseDir, path)
		if err != nil {
			return errors.Wrap(err, "failed to compute bundle path")
		}
		archivePath := filepath.ToSlash(relativePath)
		if entry.IsDir() && !strings.HasSuffix(archivePath, "/") {
			archivePath += "/"
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return errors.Wrap(err, "failed to create tar header")
		}
		header.Name = archivePath
		header.Uid = 0
		header.Gid = 0
		header.Uname = ""
		header.Gname = ""
		header.Mode = fileModeForArchivePath(archivePath, entry.IsDir(), executablePaths)

		if err := tarWriter.WriteHeader(header); err != nil {
			return errors.Wrap(err, "failed to write tar header")
		}

		if entry.IsDir() {
			return nil
		}

		sourceFile, err := os.Open(path) //nolint:gosec
		if err != nil {
			return errors.Wrap(err, "failed to open bundle source file")
		}
		defer sourceFile.Close()

		if _, err := io.Copy(tarWriter, sourceFile); err != nil {
			return errors.Wrap(err, "failed to copy file into tar archive")
		}

		return nil
	})
}

func executableArchivePaths(pluginDir string, manifest *model.Manifest) map[string]struct{} {
	executablePaths := make(map[string]struct{})
	if manifest == nil || manifest.Server == nil {
		return executablePaths
	}

	bundleRoot := normalizeArchivePath(filepath.Base(pluginDir))
	add := func(relativePath string) {
		normalized := normalizeArchivePath(relativePath)
		if normalized == "" {
			return
		}
		executablePaths[path.Join(bundleRoot, normalized)] = struct{}{}
	}

	add(manifest.Server.Executable)
	for _, executable := range manifest.Server.Executables {
		add(executable)
	}

	return executablePaths
}

func normalizeArchivePath(value string) string {
	if value == "" {
		return ""
	}
	normalized := filepath.ToSlash(filepath.Clean(value))
	return strings.TrimPrefix(normalized, "./")
}

func fileModeForArchivePath(path string, isDir bool, executablePaths map[string]struct{}) int64 {
	if isDir {
		return 0o755
	}
	if _, ok := executablePaths[normalizeArchivePath(path)]; ok {
		return 0o755
	}
	return 0o644
}
