package doctor_test

import (
	"strings"
	"testing"
)

const sensitiveUser = `package models

type User struct {
	ID       string
	Password string
}
`

// TestASecretIsReportedWhereItReachesASink: the name of a field says where to
// look, and a log line or a JSON document that carries the value is what makes
// it a leak.
func TestASecretIsReportedWhereItReachesASink(t *testing.T) {
	for _, c := range []struct {
		name  string
		files map[string]string
		want  int
		// at is a fragment of the message: the sink the finding names.
		at string
	}{
		{"a parameter handed to slog", map[string]string{
			"app/Models/User.go": sensitiveUser,
			"app/Services/UserService.go": `package services

import (
	"log/slog"

	models "example.test/shape/app/Models"
)

func audit(u models.User) { slog.Info("signed in", "user", u) }
`,
		}, 1, "slog.Info at app/Services/UserService.go:9"},
		{"a literal encoded to JSON", map[string]string{
			"app/Http/Requests/TokenRequest.go": `package requests

import "encoding/json"

type TokenRequest struct{ APIToken string }

func dump() ([]byte, error) { return json.Marshal(&TokenRequest{}) }
`,
		}, 1, "json.Marshal"},
		{"a value named like its type, on a logger field", map[string]string{
			"app/Models/Charge.go": "package models\n\ntype Charge struct{ CardToken string }\n",
			"app/Services/ChargeService.go": `package services

import "context"

type ChargeService struct {
	logger interface{ Info(string, ...any) }
	find   func(context.Context) (any, error)
}

func (s *ChargeService) Settle(ctx context.Context) {
	charge, _ := s.find(ctx)
	s.logger.Info("settled", "charge", charge)
}
`,
		}, 1, "s.logger.Info"},

		// Negatives.
		{"a secret nothing logs or encodes", map[string]string{
			"app/Models/User.go": sensitiveUser,
		}, 0, ""},
		{"a type that redacts itself", map[string]string{
			"app/Models/User.go": sensitiveUser + `
func (u User) LogValue() string { return u.ID }
`,
			"app/Services/S.go": "package services\n\nimport (\n\t\"log/slog\"\n\n\tmodels \"example.test/shape/app/Models\"\n)\n\nfunc a(u models.User) { slog.Info(\"x\", \"u\", u) }\n",
		}, 0, ""},
		{"JSON only, and the secret tagged out of it", map[string]string{
			"app/Http/Resources/UserResource.go": "package resources\n\nimport \"encoding/json\"\n\ntype UserView struct {\n\tID string `json:\"id\"`\n\tPasswordHash string `json:\"-\"`\n}\n\nfunc out(v UserView) ([]byte, error) { return json.Marshal(v) }\n",
		}, 0, ""},

		// The formerly false cases: the rule read the name of the field alone.
		{"token counts and documentation flags, logged", map[string]string{
			"app/Services/Inference.go": `package services

import (
	"log/slog"
	"time"
)

type Budget struct {
	MaxTokens    int
	TargetTokens int64
	TokenTTL     time.Duration
}

type Module struct{ documented []string }

type Doc struct{ Undocumented []string }

func report(b Budget, m Module, d Doc) { slog.Info("budget", "b", b, "m", m, "d", d) }
`,
		}, 0, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := findingsOf(t, structureProject(t, c.files), "sensitive-field-not-redacted")
			if len(got) != c.want {
				var lines []string
				for _, f := range got {
					lines = append(lines, f.String())
				}
				t.Fatalf("%d finding(s), want %d:\n%s", len(got), c.want, strings.Join(lines, "\n"))
			}
			for _, f := range got {
				if !strings.Contains(f.Message, c.at) {
					t.Errorf("the finding does not name the sink %q: %s", c.at, f.Message)
				}
			}
		})
	}
}
