package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed template/*
var embeddedAssets embed.FS

var dashboardAssets = mustSubFS(embeddedAssets, "template")

func mustSubFS(source fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(source, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

func AssetFileSystem() http.FileSystem {
	return http.FS(dashboardAssets)
}
