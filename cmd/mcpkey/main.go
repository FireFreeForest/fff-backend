// Command mcpkey manages database-backed MCP API keys from the terminal.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	gormlogger "gorm.io/gorm/logger"

	"firefreeforest/backend/internal/config"
	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/platform/postgres"
	"firefreeforest/backend/internal/repository"
	"firefreeforest/backend/internal/service"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("command is required")
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage()
		return nil
	}
	cfg, err := config.LoadMCP()
	if err != nil {
		return err
	}
	db, err := postgres.OpenWithLogger(cfg.DBDSN, gormlogger.Default.LogMode(gormlogger.Silent))
	if err != nil {
		return err
	}
	repo := repository.New(db)
	keys := service.NewMCPKeyService(repo)

	switch args[0] {
	case "create":
		return cmdCreate(context.Background(), repo, keys, args[1:])
	case "list":
		return cmdList(context.Background(), keys, args[1:])
	case "show":
		return cmdShow(context.Background(), keys, args[1:])
	case "update":
		return cmdUpdate(context.Background(), repo, keys, args[1:])
	case "rotate":
		return cmdRotate(context.Background(), repo, keys, args[1:])
	case "revoke":
		return cmdRevoke(context.Background(), repo, keys, args[1:])
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage: mcpkey <command> [flags]

Commands:
  create   Create a key and print its token once
  list     List keys
  show     Show one key
  update   Update name, role, actor, expiry, or active state
  rotate   Create a replacement key and print its token
  revoke   Disable a key

Environment:
  DB_DSN   PostgreSQL connection string (required)
`)
}

func cmdCreate(ctx context.Context, repo *repository.Repository, keys *service.MCPKeyService, args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	name := fs.String("name", "", "key name (required)")
	role := fs.String("role", "viewer", "role: admin, petugas, or viewer")
	actorID := fs.String("actor-id", "", "optional user UUID to attribute actions to")
	expiresAt := fs.String("expires-at", "", "optional expiry in RFC3339")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	actor, err := optionalUUID(*actorID)
	if err != nil {
		return fmt.Errorf("actor-id: %w", err)
	}
	expiry, err := optionalTime(*expiresAt)
	if err != nil {
		return fmt.Errorf("expires-at: %w", err)
	}
	created, err := keys.Create(ctx, service.CreateMCPKeyInput{
		Name:      *name,
		Role:      domain.UserRole(strings.ToLower(strings.TrimSpace(*role))),
		ActorID:   actor,
		ExpiresAt: expiry,
	})
	if err != nil {
		return err
	}
	auditCLI(ctx, repo, "mcp_key.create", created.Key.ID, map[string]any{
		"name": created.Key.Name,
		"role": created.Key.Role,
	})
	return printCreated(*jsonOut, created)
}

func cmdList(ctx context.Context, keys *service.MCPKeyService, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	page := fs.Int("page", 1, "page number")
	perPage := fs.Int("per-page", 20, "items per page (max 100)")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *page < 1 {
		*page = 1
	}
	if *perPage < 1 {
		*perPage = 20
	}
	if *perPage > 100 {
		*perPage = 100
	}
	rows, total, err := keys.List(ctx, *perPage, (*page-1)**perPage)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(map[string]any{
			"data": rows,
			"meta": map[string]any{"page": *page, "per_page": *perPage, "total": total},
		})
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tPREFIX\tROLE\tACTIVE\tEXPIRES\tLAST USED")
	for _, key := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%t\t%s\t%s\n",
			key.ID,
			key.Name,
			key.KeyPrefix,
			key.Role,
			key.IsActive,
			formatTime(key.ExpiresAt),
			formatTime(key.LastUsedAt),
		)
	}
	_ = tw.Flush()
	fmt.Printf("\n%d key(s), page %d\n", total, *page)
	return nil
}

func cmdShow(ctx context.Context, keys *service.MCPKeyService, args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	idRaw := fs.String("id", "", "key UUID (required)")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	id, err := requiredUUID(*idRaw, "id")
	if err != nil {
		return err
	}
	key, err := keys.Get(ctx, id)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(key)
	}
	fmt.Printf("id:           %s\n", key.ID)
	fmt.Printf("name:         %s\n", key.Name)
	fmt.Printf("prefix:       %s\n", key.KeyPrefix)
	fmt.Printf("role:         %s\n", key.Role)
	fmt.Printf("actor_id:     %s\n", formatUUID(key.ActorID))
	fmt.Printf("active:       %t\n", key.IsActive)
	fmt.Printf("expires_at:   %s\n", formatTime(key.ExpiresAt))
	fmt.Printf("last_used_at: %s\n", formatTime(key.LastUsedAt))
	fmt.Printf("created_at:   %s\n", key.CreatedAt.Format(time.RFC3339))
	return nil
}

func cmdUpdate(ctx context.Context, repo *repository.Repository, keys *service.MCPKeyService, args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	idRaw := fs.String("id", "", "key UUID (required)")
	name := fs.String("name", "", "new name")
	role := fs.String("role", "", "new role: admin, petugas, or viewer")
	actorID := fs.String("actor-id", "", "new actor user UUID")
	clearActor := fs.Bool("clear-actor-id", false, "remove the actor link")
	expiresAt := fs.String("expires-at", "", "new expiry in RFC3339")
	clearExpiry := fs.Bool("clear-expires-at", false, "remove the expiry")
	enable := fs.Bool("enable", false, "activate the key")
	disable := fs.Bool("disable", false, "deactivate the key")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	id, err := requiredUUID(*idRaw, "id")
	if err != nil {
		return err
	}
	if *enable && *disable {
		return errors.New("enable and disable cannot be used together")
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	in := service.UpdateMCPKeyInput{
		ClearActor:  *clearActor,
		ClearExpiry: *clearExpiry,
	}
	if set["name"] {
		in.Name = name
	}
	if set["role"] {
		parsed := domain.UserRole(strings.ToLower(strings.TrimSpace(*role)))
		in.Role = &parsed
	}
	if set["actor-id"] {
		parsed, err := optionalUUID(*actorID)
		if err != nil {
			return fmt.Errorf("actor-id: %w", err)
		}
		in.ActorID = parsed
	}
	if set["expires-at"] {
		parsed, err := optionalTime(*expiresAt)
		if err != nil {
			return fmt.Errorf("expires-at: %w", err)
		}
		in.ExpiresAt = parsed
	}
	if *enable {
		active := true
		in.IsActive = &active
	}
	if *disable {
		active := false
		in.IsActive = &active
	}

	updated, err := keys.Update(ctx, id, in)
	if err != nil {
		return err
	}
	auditCLI(ctx, repo, "mcp_key.update", id, nil)
	if *jsonOut {
		return printJSON(updated)
	}
	fmt.Printf("updated key %s (%s), active=%t\n", updated.ID, updated.Name, updated.IsActive)
	return nil
}

func cmdRotate(ctx context.Context, repo *repository.Repository, keys *service.MCPKeyService, args []string) error {
	fs := flag.NewFlagSet("rotate", flag.ContinueOnError)
	idRaw := fs.String("id", "", "key UUID (required)")
	graceRaw := fs.String("grace", "0s", "how long the old key remains valid, e.g. 1h")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	id, err := requiredUUID(*idRaw, "id")
	if err != nil {
		return err
	}
	grace, err := time.ParseDuration(*graceRaw)
	if err != nil || grace < 0 || grace > 7*24*time.Hour {
		return fmt.Errorf("grace must be a duration between 0 and 168h")
	}
	created, previous, err := keys.Rotate(ctx, id, grace)
	if err != nil {
		return err
	}
	auditCLI(ctx, repo, "mcp_key.rotate", id, map[string]any{
		"new_key_id": created.Key.ID.String(),
		"grace":      grace.String(),
	})
	if *jsonOut {
		return printJSON(map[string]any{
			"key":      created.Key,
			"token":    created.Token,
			"previous": previous,
		})
	}
	fmt.Printf("rotated key %s\n", id)
	fmt.Printf("new key id: %s\n", created.Key.ID)
	fmt.Printf("old key expires_at: %s\n", formatTime(previous.ExpiresAt))
	fmt.Printf("\ntoken (shown once): %s\n", created.Token)
	return nil
}

func cmdRevoke(ctx context.Context, repo *repository.Repository, keys *service.MCPKeyService, args []string) error {
	fs := flag.NewFlagSet("revoke", flag.ContinueOnError)
	idRaw := fs.String("id", "", "key UUID (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	id, err := requiredUUID(*idRaw, "id")
	if err != nil {
		return err
	}
	active := false
	key, err := keys.Update(ctx, id, service.UpdateMCPKeyInput{IsActive: &active})
	if err != nil {
		return err
	}
	auditCLI(ctx, repo, "mcp_key.revoke", id, nil)
	fmt.Printf("revoked key %s (%s)\n", key.ID, key.Name)
	return nil
}

func printCreated(jsonOut bool, created *service.CreatedMCPKey) error {
	if jsonOut {
		return printJSON(map[string]any{
			"key":   created.Key,
			"token": created.Token,
		})
	}
	fmt.Printf("created key %s\n", created.Key.ID)
	fmt.Printf("name:   %s\n", created.Key.Name)
	fmt.Printf("role:   %s\n", created.Key.Role)
	fmt.Printf("prefix: %s\n", created.Key.KeyPrefix)
	fmt.Printf("expires_at: %s\n", formatTime(created.Key.ExpiresAt))
	fmt.Printf("\ntoken (shown once): %s\n", created.Token)
	fmt.Println("store this token now; it cannot be recovered later")
	return nil
}

func auditCLI(ctx context.Context, repo *repository.Repository, action string, id uuid.UUID, meta map[string]any) {
	payload := map[string]any{"via": "cli"}
	for k, v := range meta {
		payload[k] = v
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = repo.CreateAuditLog(ctx, &domain.AuditLog{
		Action:    action,
		Entity:    "mcp_key",
		EntityID:  id.String(),
		Metadata:  raw,
		IP:        "cli",
		CreatedAt: time.Now().UTC(),
	})
}

func printJSON(v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(raw))
	return nil
}

func requiredUUID(raw, field string) (uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return uuid.Nil, fmt.Errorf("%s is required", field)
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%s must be a UUID", field)
	}
	return id, nil
}

func optionalUUID(raw string) (*uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, errors.New("must be a UUID")
	}
	return &id, nil
}

func optionalTime(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, errors.New("must be RFC3339")
	}
	t = t.UTC()
	return &t, nil
}

func formatTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func formatUUID(id *uuid.UUID) string {
	if id == nil {
		return "-"
	}
	return id.String()
}
