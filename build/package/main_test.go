package main

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func TestExecutableArchivePaths(t *testing.T) {
	t.Parallel()

	manifest := &model.Manifest{
		Id: "com.example.analytics",
		Server: &model.ManifestServer{
			Executable: "server/dist/plugin-linux-amd64",
			Executables: map[string]string{
				"linux-arm64": "./server/dist/plugin-linux-arm64",
			},
		},
	}

	paths := executableArchivePaths(filepath.Join("dist", manifest.Id), manifest)

	require.Contains(t, paths, "com.example.analytics/server/dist/plugin-linux-amd64")
	require.Contains(t, paths, "com.example.analytics/server/dist/plugin-linux-arm64")
}

func TestPackagePluginSetsExecutableModesForConfiguredServerBinaries(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	pluginDir := filepath.Join(rootDir, "dist", "com.example.analytics")
	require.NoError(t, os.MkdirAll(filepath.Join(pluginDir, "server", "dist"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(pluginDir, "webapp", "dist"), 0o755))

	serverBinary := filepath.Join(pluginDir, "server", "dist", "plugin-linux-amd64")
	serverBinaryArm := filepath.Join(pluginDir, "server", "dist", "plugin-linux-arm64")
	webBundle := filepath.Join(pluginDir, "webapp", "dist", "main.js")

	require.NoError(t, os.WriteFile(serverBinary, []byte("server-binary"), 0o644))
	require.NoError(t, os.WriteFile(serverBinaryArm, []byte("server-binary-arm"), 0o644))
	require.NoError(t, os.WriteFile(webBundle, []byte("console.log('ok');"), 0o644))

	manifest := &model.Manifest{
		Id:      "com.example.analytics",
		Version: "1.2.3",
		Server: &model.ManifestServer{
			Executable: "server/dist/plugin-linux-amd64",
			Executables: map[string]string{
				"linux-arm64": "server/dist/plugin-linux-arm64",
			},
		},
	}

	bundlePath := filepath.Join(rootDir, "dist", "com.example.analytics-1.2.3.tar.gz")
	require.NoError(t, packagePlugin(pluginDir, bundlePath, manifest))

	bundleFile, err := os.Open(bundlePath)
	require.NoError(t, err)
	defer bundleFile.Close()

	gzipReader, err := gzip.NewReader(bundleFile)
	require.NoError(t, err)
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	modes := make(map[string]int64)
	for {
		header, readErr := tarReader.Next()
		if readErr != nil {
			require.ErrorIs(t, readErr, io.EOF)
			break
		}
		modes[header.Name] = header.Mode
	}

	require.Equal(t, int64(0o755), modes["com.example.analytics/"])
	require.Equal(t, int64(0o755), modes["com.example.analytics/server/"])
	require.Equal(t, int64(0o755), modes["com.example.analytics/server/dist/"])
	require.Equal(t, int64(0o755), modes["com.example.analytics/server/dist/plugin-linux-amd64"])
	require.Equal(t, int64(0o755), modes["com.example.analytics/server/dist/plugin-linux-arm64"])
	require.Equal(t, int64(0o644), modes["com.example.analytics/webapp/dist/main.js"])
}
