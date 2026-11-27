// Package mcp implements the Model Context Protocol transports for Fire Free
// Forest. The same read-only tool catalog is exposed over Streamable HTTP and
// stdio.
package mcp

import (
	"log/slog"
	"net/http"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	goredis "github.com/redis/go-redis/v9"

	"firefreeforest/backend/internal/config"
	"firefreeforest/backend/internal/repository"
	"firefreeforest/backend/internal/service"
)

const serverName = "fire-free-forest"

// Deps contains everything the MCP tools need. It deliberately reuses the same
// repository and service layer as the REST API.
type Deps struct {
	Config   *config.Config
	Repo     *repository.Repository
	Geocoder *service.Geocoder
	Redis    *goredis.Client
	Logger   *slog.Logger
	Version  string
}

// NewServer builds the read-only MCP server. MCP never mutates data.
func NewServer(d Deps) *sdkmcp.Server {
	if d.Version == "" {
		d.Version = "1.0.0"
	}
	s := sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    serverName,
		Version: d.Version,
	}, &sdkmcp.ServerOptions{
		Instructions: instructions(),
		Logger:       d.Logger,
	})

	registerReadTools(s, d)
	registerResources(s, d)
	registerPrompts(s, d)
	return s
}

// NewHTTPHandler returns the authenticated Streamable HTTP handler. Requests
// must present MCP_API_KEY as a Bearer token.
func NewHTTPHandler(d Deps, token string, rateLimitPerMin int) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	server := NewServer(d)
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server {
		return server
	}, &sdkmcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		Logger:       d.Logger,
	})
	return AuthMiddleware(token, d.Redis, rateLimitPerMin, d.Logger)(handler)
}

func instructions() string {
	return `Anda adalah asisten operasional Fire Free Forest dengan akses baca-saja.
Gunakan tool baca untuk memeriksa perangkat, telemetri, event, command, dan ringkasan dashboard.
Selalu tampilkan waktu dalam RFC3339 UTC dan sebutkan device_id publik, bukan UUID internal.
Jika data tidak ditemukan, laporkan apa adanya; jangan mengarang.
Kredensial ini tidak dapat mengubah data: jangan menyarankan tindakan yang tidak tersedia; cukup berikan analisis dan rekomendasi.`
}
