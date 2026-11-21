// Package mcp implements the Model Context Protocol transports for Fire Free
// Forest. The same tool catalog is exposed over Streamable HTTP and stdio.
package mcp

import (
	"fmt"
	"log/slog"
	"net/http"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	goredis "github.com/redis/go-redis/v9"

	"firefreeforest/backend/internal/config"
	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/repository"
	"firefreeforest/backend/internal/service"
)

const serverName = "fire-free-forest"

// Deps contains everything the MCP tools need. It deliberately reuses the same
// repository and service layer as the REST API.
type Deps struct {
	Config   *config.Config
	Repo     *repository.Repository
	Commands *service.CommandService
	Geocoder *service.Geocoder
	Keys     *service.MCPKeyService
	Redis    *goredis.Client
	Logger   *slog.Logger
	Version  string
}

type ServerSet struct {
	servers map[domain.UserRole]*sdkmcp.Server
}

// NewServer builds one MCP server with the tools allowed for role.
func NewServer(d Deps, role domain.UserRole) *sdkmcp.Server {
	if d.Version == "" {
		d.Version = "1.0.0"
	}
	s := sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    serverName,
		Version: d.Version,
	}, &sdkmcp.ServerOptions{
		Instructions: instructionsFor(role),
		Logger:       d.Logger,
	})

	registerReadTools(s, d, role)
	if role == domain.RolePetugas || role == domain.RoleAdmin {
		registerOperationalTools(s, d, role)
	}
	registerResources(s, d, role)
	registerPrompts(s, d, role)
	return s
}

// NewServerSet builds one server per role so tools/list is always scoped to the
// authenticated key.
func NewServerSet(d Deps) *ServerSet {
	set := &ServerSet{servers: make(map[domain.UserRole]*sdkmcp.Server, 3)}
	for _, role := range []domain.UserRole{domain.RoleViewer, domain.RolePetugas, domain.RoleAdmin} {
		set.servers[role] = NewServer(d, role)
	}
	return set
}

func (s *ServerSet) ForRole(role domain.UserRole) *sdkmcp.Server {
	if srv, ok := s.servers[role]; ok {
		return srv
	}
	return s.servers[domain.RoleViewer]
}

// NewHTTPHandler returns the authenticated Streamable HTTP handler. The MCP
// handler selects a server based on the key role placed in the request context
// by AuthMiddleware.
func NewHTTPHandler(d Deps, rateLimitPerMin int) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	set := NewServerSet(d)
	handler := sdkmcp.NewStreamableHTTPHandler(func(r *http.Request) *sdkmcp.Server {
		principal := PrincipalFromContext(r.Context())
		if principal == nil {
			return set.ForRole(domain.RoleViewer)
		}
		return set.ForRole(principal.Role)
	}, &sdkmcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		Logger:       d.Logger,
	})
	return AuthMiddleware(d.Keys, d.Redis, rateLimitPerMin, d.Logger)(handler)
}

func instructionsFor(role domain.UserRole) string {
	base := fmt.Sprintf(`Anda adalah asisten operasional Fire Free Forest. Peran kredensial ini: %s.
Gunakan tool baca untuk memeriksa perangkat, telemetri, event, command, dan ringkasan dashboard.
Selalu tampilkan waktu dalam RFC3339 UTC dan sebutkan device_id publik, bukan UUID internal.
Jika data tidak ditemukan, laporkan apa adanya; jangan mengarang.`, role)
	switch role {
	case domain.RoleViewer:
		return base + `
Kredensial ini hanya boleh membaca. Jangan menyarankan tindakan yang tidak tersedia; cukup berikan analisis dan rekomendasi.`
	case domain.RolePetugas:
		return base + `
Kredensial ini boleh mengakui event, menyelesaikan event, dan mengirim perintah pompa.
Sebelum mengirim perintah pompa, pastikan device benar dan gunakan idempotency_key agar retry aman.`
	default:
		return base + `
Kredensial ini boleh mengakui event dan mengirim perintah pompa. Penyelesaian event adalah wewenang petugas.
Sebelum mengirim perintah pompa, pastikan device benar dan gunakan idempotency_key agar retry aman.`
	}
}
