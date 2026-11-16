package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestParseAggregateParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		query      string
		wantOK     bool
		wantStatus int
		wantError  string
		wantFrom   string
		wantTo     string
		wantBucket int
	}{
		{
			name:       "valid default interval",
			query:      "from=2026-01-01T00:00:00Z&to=2026-01-01T00:10:00Z",
			wantOK:     true,
			wantFrom:   "2026-01-01T00:00:00Z",
			wantTo:     "2026-01-01T00:10:00Z",
			wantBucket: 5,
		},
		{
			name:       "valid explicit interval",
			query:      "from=2026-01-01T00:00:00Z&to=2026-01-01T00:10:00Z&interval=1",
			wantOK:     true,
			wantFrom:   "2026-01-01T00:00:00Z",
			wantTo:     "2026-01-01T00:10:00Z",
			wantBucket: 1,
		},
		{
			name:       "missing from",
			query:      "to=2026-01-01T00:10:00Z",
			wantStatus: http.StatusUnprocessableEntity,
			wantError:  "from dan to wajib diisi",
		},
		{
			name:       "missing to",
			query:      "from=2026-01-01T00:00:00Z",
			wantStatus: http.StatusUnprocessableEntity,
			wantError:  "from dan to wajib diisi",
		},
		{
			name:       "invalid from",
			query:      "from=kemarin&to=2026-01-01T00:10:00Z",
			wantStatus: http.StatusBadRequest,
			wantError:  "Format waktu 'from' harus RFC3339",
		},
		{
			name:       "from not before to",
			query:      "from=2026-01-01T00:10:00Z&to=2026-01-01T00:10:00Z",
			wantStatus: http.StatusUnprocessableEntity,
			wantError:  "from harus lebih awal dari to",
		},
		{
			name:       "interval too small",
			query:      "from=2026-01-01T00:00:00Z&to=2026-01-01T00:10:00Z&interval=0",
			wantStatus: http.StatusUnprocessableEntity,
			wantError:  "interval harus antara 1 dan 1440 menit",
		},
		{
			name:       "interval invalid",
			query:      "from=2026-01-01T00:00:00Z&to=2026-01-01T00:10:00Z&interval=lima",
			wantStatus: http.StatusUnprocessableEntity,
			wantError:  "interval harus antara 1 dan 1440 menit",
		},
		{
			name:       "range too large",
			query:      "from=2026-01-01T00:00:00Z&to=2026-04-02T00:00:00Z&interval=1440",
			wantStatus: http.StatusUnprocessableEntity,
			wantError:  "Rentang waktu maksimal 90 hari",
		},
		{
			name:       "too many buckets",
			query:      "from=2026-01-01T00:00:00Z&to=2026-01-06T00:00:00Z&interval=1",
			wantStatus: http.StatusUnprocessableEntity,
			wantError:  "Jumlah bucket maksimal 5000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodGet, "/telemetry?"+tt.query, nil)

			params, ok := parseAggregateParams(c)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				if rec.Code != tt.wantStatus {
					t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
				}
				if !strings.Contains(rec.Body.String(), tt.wantError) {
					t.Fatalf("body = %s, want contains %q", rec.Body.String(), tt.wantError)
				}
				return
			}
			if params.from.Format("2006-01-02T15:04:05Z07:00") != tt.wantFrom {
				t.Fatalf("from = %s, want %s", params.from, tt.wantFrom)
			}
			if params.to.Format("2006-01-02T15:04:05Z07:00") != tt.wantTo {
				t.Fatalf("to = %s, want %s", params.to, tt.wantTo)
			}
			if params.intervalMinutes != tt.wantBucket {
				t.Fatalf("interval = %d, want %d", params.intervalMinutes, tt.wantBucket)
			}
		})
	}
}
