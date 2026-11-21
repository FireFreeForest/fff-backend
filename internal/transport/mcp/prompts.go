package mcp

import (
	"context"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"firefreeforest/backend/internal/domain"
)

func registerPrompts(s *sdkmcp.Server, _ Deps, role domain.UserRole) {
	s.AddPrompt(&sdkmcp.Prompt{
		Name:        "triage_events",
		Description: "Susun prioritas penanganan event aktif berdasarkan severity, umur event, dan kondisi perangkat.",
		Arguments: []*sdkmcp.PromptArgument{{
			Name:        "severity",
			Description: "Batasi ke severity tertentu: error, warning, atau critical (opsional).",
			Required:    false,
		}},
	}, func(_ context.Context, req *sdkmcp.GetPromptRequest) (*sdkmcp.GetPromptResult, error) {
		severity := strings.ToLower(strings.TrimSpace(req.Params.Arguments["severity"]))
		filter := ""
		if severity != "" {
			filter = fmt.Sprintf(" Filter severity=%s.", severity)
		}
		return promptResult("Triage event aktif", fmt.Sprintf(`Gunakan tool list_events dengan status=active dan per_page=100.%s
Untuk setiap event, periksa device terkait memakai get_device dan get_device_status.
Susun tabel prioritas berisi event_id, device_id, tipe, severity, waktu kejadian, status, dan risk level perangkat.
Urutkan dari risiko tertinggi ke terendah, lalu jelaskan langkah operasional berikutnya.
Jika ada event FIRE_DETECTED atau SMOKE_DETECTED, sebutkan secara eksplisit di bagian paling atas.`, filter)), nil
	})

	s.AddPrompt(&sdkmcp.Prompt{
		Name:        "device_health",
		Description: "Analisis kesehatan satu perangkat dari status, telemetri terbaru, event aktif, dan command terakhir.",
		Arguments: []*sdkmcp.PromptArgument{{
			Name:        "device_id",
			Description: "device_id publik perangkat.",
			Required:    true,
		}},
	}, func(_ context.Context, req *sdkmcp.GetPromptRequest) (*sdkmcp.GetPromptResult, error) {
		deviceID := strings.TrimSpace(req.Params.Arguments["device_id"])
		if deviceID == "" {
			return nil, fmt.Errorf("argumen device_id wajib diisi")
		}
		return promptResult("Pemeriksaan kesehatan perangkat", fmt.Sprintf(`Periksa perangkat %s secara menyeluruh:
1. Panggil get_device dan get_device_status.
2. Panggil get_latest_telemetry.
3. Panggil list_events dengan device_id=%s, per_page=20.
4. Panggil list_commands dengan device_id=%s, per_page=10.
Rangkum kondisi perangkat, anomali telemetri, event yang belum selesai, dan command yang gagal atau timeout.
Berikan rekomendasi tindakan yang bisa dilakukan tanpa mengubah konfigurasi perangkat.`, deviceID, deviceID, deviceID)), nil
	})

	s.AddPrompt(&sdkmcp.Prompt{
		Name:        "shift_briefing",
		Description: "Buat ringkasan serah-terima shift dari kondisi dashboard, event aktif, dan perangkat offline.",
	}, func(_ context.Context, _ *sdkmcp.GetPromptRequest) (*sdkmcp.GetPromptResult, error) {
		return promptResult("Briefing shift", `Panggil get_dashboard_summary, lalu list_events dengan status=active dan per_page=100.
Panggil list_devices dan identifikasi perangkat yang offline atau tidak mengirim telemetri.
Susun briefing singkat berisi: jumlah perangkat online/offline, distribusi risk, event aktif per severity, daftar event paling mendesak, dan hal yang perlu dipantau pada shift berikutnya.
Jangan mengarang data yang tidak dikembalikan tool.`), nil
	})

	if role == domain.RolePetugas || role == domain.RoleAdmin {
		s.AddPrompt(&sdkmcp.Prompt{
			Name:        "pump_command_checklist",
			Description: "Checklist sebelum mengirim perintah pompa ke perangkat.",
			Arguments: []*sdkmcp.PromptArgument{{
				Name:        "device_id",
				Description: "device_id publik perangkat.",
				Required:    true,
			}},
		}, func(_ context.Context, req *sdkmcp.GetPromptRequest) (*sdkmcp.GetPromptResult, error) {
			deviceID := strings.TrimSpace(req.Params.Arguments["device_id"])
			if deviceID == "" {
				return nil, fmt.Errorf("argumen device_id wajib diisi")
			}
			return promptResult("Checklist perintah pompa", fmt.Sprintf(`Sebelum mengirim perintah pompa ke %s, lakukan pemeriksaan berikut:
1. get_device dan get_device_status untuk memastikan perangkat online.
2. get_latest_telemetry untuk melihat kondisi terbaru.
3. list_events dengan device_id=%s dan status=active untuk memastikan konteks kebakaran.
4. list_commands dengan device_id=%s untuk melihat command terakhir dan menghindari pengulangan.
Jika perangkat offline atau data tidak konsisten, jangan kirim command; laporkan alasannya.
Jika aman, gunakan issue_pump_command dengan state dan duration yang jelas, serta idempotency_key unik.`, deviceID, deviceID, deviceID)), nil
		})
	}
}

func promptResult(description, text string) *sdkmcp.GetPromptResult {
	return &sdkmcp.GetPromptResult{
		Description: description,
		Messages: []*sdkmcp.PromptMessage{{
			Role:    sdkmcp.Role("user"),
			Content: &sdkmcp.TextContent{Text: text},
		}},
	}
}
