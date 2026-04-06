package web

import (
	"embed"
	"io/fs"

	"github.com/versionlens/OpenNalvin/internal/server"
)

//go:embed all:dist
var assets embed.FS

func Source() server.AssetSource {
	index, err := assets.ReadFile("dist/index.html")
	if err != nil {
		return server.AssetSource{}
	}

	staticFS, err := fs.Sub(assets, "dist")
	if err != nil {
		return server.AssetSource{}
	}

	return server.AssetSource{
		Files:    staticFS,
		Index:    index,
		Embedded: true,
	}
}
