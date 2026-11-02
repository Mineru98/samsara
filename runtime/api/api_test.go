package api

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Mineru98/samsara/runtime/internal/app"
	"github.com/Mineru98/samsara/runtime/internal/backend"
	"github.com/Mineru98/samsara/runtime/internal/config"
	"github.com/Mineru98/samsara/runtime/internal/domain"
	"github.com/Mineru98/samsara/runtime/internal/plan"
)

var update = flag.Bool("update", false, "rewrite generated TypeScript")

func TestSchemasCompile(t *testing.T) {
	for _, f := range SchemaFiles() {
		doc, err := SchemaDoc(f)
		if err != nil {
			t.Fatal(err)
		}
		if doc["$id"] != BaseURL+f {
			t.Errorf("%s: $id = %v", f, doc["$id"])
		}
		defs, _ := doc["$defs"].(map[string]any)
		for n := range defs {
			for _, closed := range []bool{false, true} {
				v := tolerant
				if closed {
					v = strict
				}
				if _, err := v.schema(Ref(f, n), closed); err != nil {
					t.Errorf("%s: %v", Ref(f, n), err)
				}
			}
		}
	}
}

func TestStrictRejectsUndeclaredFields(t *testing.T) {
	ok := []byte(`{"code":"CONFLICT","message":"m","retryable":false}`)
	extra := []byte(`{"code":"CONFLICT","message":"m","retryable":false,"hint":"x"}`)
	ref := Ref("common.schema.json", "ErrorDetail")
	if err := ValidateStrict(ref, ok); err != nil {
		t.Fatal(err)
	}
	if err := Validate(ref, extra); err != nil {
		t.Fatalf("clients must accept additive fields: %v", err)
	}
	if err := ValidateStrict(ref, extra); err == nil {
		t.Fatal("strict validation must reject undeclared fields")
	}
	if err := ValidateStrict(Ref("common.schema.json", "Decimal"), []byte(`42`)); err == nil {
		t.Fatal("int64 values must be decimal strings")
	}
}

type fixture struct {
	Name     string `json:"name"`
	Payloads []struct {
		Schema string          `json:"schema"`
		Note   string          `json:"note"`
		Expect string          `json:"expect"`
		Data   json.RawMessage `json:"data"`
	} `json:"payloads"`
}

func loadFixtures(t *testing.T) map[string]fixture {
	t.Helper()
	files, _ := filepath.Glob("fixtures/*.json")
	out := map[string]fixture{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var fx fixture
		if err := json.Unmarshal(b, &fx); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if fx.Name+".json" != filepath.Base(f) || len(fx.Payloads) == 0 {
			t.Fatalf("%s: name %q or empty payloads", f, fx.Name)
		}
		out[fx.Name] = fx
	}
	return out
}

// TestFixtures checks every shared fixture against its schema. The same files
// drive ts/contract.test.ts.
func TestFixtures(t *testing.T) {
	fxs := loadFixtures(t)
	for _, want := range []string{"abc-partial-block", "stale-approval", "merged-history", "unknown-delivery", "unknown-enum", "http-errors"} {
		if _, ok := fxs[want]; !ok {
			t.Errorf("fixture %s missing", want)
		}
	}
	for name, fx := range fxs {
		for i, p := range fx.Payloads {
			strictErr := ValidateStrict(p.Schema, p.Data)
			looseErr := Validate(p.Schema, p.Data)
			switch p.Expect {
			case "":
				if strictErr != nil {
					t.Errorf("%s[%d] %s: %v", name, i, p.Note, strictErr)
				}
			case "tolerant":
				if looseErr != nil || strictErr == nil {
					t.Errorf("%s[%d] %s: tolerant=%v strict=%v; want accepted by clients only", name, i, p.Note, looseErr, strictErr)
				}
			case "reject":
				// Must fail on the enum itself, not for an unrelated reason.
				if looseErr == nil || !strings.Contains(looseErr.Error(), "must be one of") {
					t.Errorf("%s[%d] %s: want an enum violation, got %v", name, i, p.Note, looseErr)
				}
			default:
				t.Errorf("%s[%d]: unknown expect %q", name, i, p.Expect)
			}
		}
	}
	// Each scenario must actually show what it is named after.
	for name, needle := range map[string]string{
		"abc-partial-block": `"display_status":"BLOCKED"`,
		"stale-approval":    `"state":"INVALIDATED"`,
		"merged-history":    `"state":"MERGED"`,
		"unknown-delivery":  `"state":"UNKNOWN"`,
		"unknown-enum":      `"HIBERNATING"`,
		"http-errors":       `"code":"SESSION_EXPIRED"`,
	} {
		found := false
		for _, p := range fxs[name].Payloads {
			var c bytes.Buffer
			_ = json.Compact(&c, p.Data)
			found = found || strings.Contains(c.String(), needle)
		}
		if !found {
			t.Errorf("fixture %s does not contain %s", name, needle)
		}
	}
}

func enumOf(t *testing.T, file, def string) []string {
	t.Helper()
	doc, err := SchemaDoc(file)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := doc["$defs"].(map[string]any)[def].(map[string]any)
	vals, _ := s["enum"].([]any)
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		out = append(out, v.(string))
	}
	sort.Strings(out)
	return out
}

// TestEnumsMatchDomain compares each schema enum with the Go constants listed
// here. Go has no reflection over constants, so a new constant must be added
// to this list too; then the schema, the regenerated TS and the client
// unknown-handling test have to follow (03 §12.1).
func TestEnumsMatchDomain(t *testing.T) {
	cases := map[string][]string{
		"ProjectActivation":   {domain.ActivationUnconfigured, domain.ActivationEnabled, domain.ActivationDisabled},
		"RegistryState":       {domain.RegistryPresent, domain.RegistryMissing, domain.RegistryArchived},
		"DrainState":          {domain.DrainNone, domain.DrainRequested, domain.DrainDraining, domain.DrainComplete, domain.DrainNeedsAttention},
		"RunState":            {domain.RunPlanned, domain.RunActive, domain.RunPaused, domain.RunCompleted, domain.RunFailed, domain.RunCancelled},
		"IssueLifecycle":      {domain.IssueOpen, domain.IssueCompleted, domain.IssueCancelled},
		"BackendAvailability": {domain.BackendAvailable, domain.BackendDegraded, domain.BackendUnavailable},
		"OperationState":      {domain.OpPending, domain.OpRunning, domain.OpSucceeded, domain.OpFailed, domain.OpUnknown, domain.OpCancelled},
		"AttemptState": {domain.AttemptPending, domain.AttemptStarting, domain.AttemptRunning, domain.AttemptPausing, domain.AttemptPaused,
			domain.AttemptImplemented, domain.AttemptVerifying, domain.AttemptVerified, domain.AttemptFailed, domain.AttemptCancelRequested,
			domain.AttemptCancelled, domain.AttemptLost},
		"SessionDesiredState": {domain.SessionDesiredActive, domain.SessionDesiredPaused, domain.SessionDesiredStopped},
		"Liveness":            {domain.LivenessAlive, domain.LivenessExited, domain.LivenessUnknown},
		"InputState":          {domain.InputReady, domain.InputBusy, domain.InputNativePermission, domain.InputUserOwned, domain.InputUnknown},
		"LeaseState":          {domain.LeaseActive, domain.LeaseRevoked, domain.LeaseExpired, domain.LeaseReleased},
		"ApprovalKind":        {domain.ApprovalPlan, domain.ApprovalResult, domain.ApprovalMerge, domain.ApprovalRecovery},
		"ApprovalState":       {domain.ApprovalPending, domain.ApprovalApproved, domain.ApprovalRejected, domain.ApprovalInvalidated, domain.ApprovalCancelled},
		"CandidateState": {domain.CandidateWaiting, domain.CandidateValidating, domain.CandidateReady, domain.CandidatePublishing,
			domain.CandidateMerged, domain.CandidateStaleBase, domain.CandidateConflict, domain.CandidateFailed, domain.CandidateCancelled},
		"NativeQueueState": {domain.NativeNone, domain.NativeRequested, domain.NativeQueued, domain.NativeValidating, domain.NativeMerged,
			domain.NativeRemoved, domain.NativeUnknown},
		"Milestone":            {domain.MilestoneImplemented, domain.MilestoneVerified, domain.MilestoneResultApprove, domain.MilestoneIntegrated},
		"DownstreamPhase":      {domain.PhaseExecution, domain.PhaseIntegration},
		"IntegrationAuthority": {domain.AuthorityManual, domain.AuthorityGitHubQueue, domain.AuthoritySamsaraLocal},
		"WorktreeState": {domain.WorktreeCreating, domain.WorktreeReady, domain.WorktreeInUse, domain.WorktreeQuiescing, domain.WorktreeRetained,
			domain.WorktreeCleanupEligible, domain.WorktreeRemoved, domain.WorktreeQuarantined},
		"Ownership":   {domain.OwnershipManaged, domain.OwnershipExternal},
		"BackendKind": {domain.BackendHerdr, domain.BackendTmux, domain.BackendStandalone, domain.BackendFake},
		"Freshness":   {domain.FreshLive, domain.FreshPartial, domain.FreshStale, domain.FreshUnavailable},
		"ErrorCode": {domain.CodeInvalidRequest, domain.CodeSessionExpired, domain.CodeActionForbidden, domain.CodeNotFound,
			domain.CodeDependencyUnmet, domain.CodeDeliveryUnknown, domain.CodeIdempotencyKeyReused, domain.CodeConflict,
			domain.CodeVersionMismatch, domain.CodeApprovalSubjectChanged, domain.CodeApprovalRequired, domain.CodeUnsupportedCapability,
			domain.CodeRateLimited, domain.CodeRuntimeNotReady, domain.CodeStorageUnavailable, domain.CodeRunNotFinalized,
			domain.CodeProtocolMismatch, domain.CodeProjectNotEnabled, domain.CodeProjectNotTrusted, domain.CodeCycleDetected,
			domain.CodeOwnershipConflict, domain.CodeTargetCheckedOut, domain.CodeStaleFence, domain.CodeInternal},
	}
	for def, want := range cases {
		sort.Strings(want)
		if got := enumOf(t, "common.schema.json", def); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: schema %v, Go %v", def, got, want)
		}
	}
	caps := make([]string, 0, len(plan.KnownCapabilities))
	for c := range plan.KnownCapabilities {
		caps = append(caps, c)
	}
	sort.Strings(caps)
	if got := enumOf(t, "plan.schema.json", "PlanCapability"); strings.Join(got, ",") != strings.Join(caps, ",") {
		t.Errorf("PlanCapability: schema %v, Go %v", got, caps)
	}
	levels := []string{string(backend.Supported), string(backend.Conditional), string(backend.Unsupported), ""} // "" = not probed
	sort.Strings(levels)
	doc, _ := SchemaDoc("entities.schema.json")
	lv := doc["$defs"].(map[string]any)["Capability"].(map[string]any)["properties"].(map[string]any)["level"].(map[string]any)["enum"].([]any)
	var got []string
	for _, v := range lv {
		got = append(got, v.(string))
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(levels, ",") {
		t.Errorf("Capability.level: schema %v, Go %v", got, levels)
	}
}

func readSource(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestOpenAPIMatchesServer keeps openapi.yaml in step with the routes the
// HTTP server registers and checks every schema reference resolves.
func TestOpenAPIMatchesServer(t *testing.T) {
	b, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		OpenAPI string                               `yaml:"openapi"`
		Paths   map[string]map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.OpenAPI != "3.1.0" {
		t.Fatalf("openapi = %q", doc.OpenAPI)
	}
	documented := map[string]bool{}
	for p, ops := range doc.Paths {
		for m, op := range ops {
			documented[strings.ToUpper(m)+" "+p] = true
			resp, _ := op["responses"].(map[string]any)
			if _, ok := resp["default"]; !ok {
				t.Errorf("%s %s: no default error response", m, p)
			}
		}
	}
	re := regexp.MustCompile(`HandleFunc\("(GET|POST|DELETE|PUT|PATCH) (/v1/[^"]+)"`)
	served := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(readSource(t, "internal/transport/httpapi/server.go"), -1) {
		served[m[1]+" "+m[2]] = true
	}
	if len(served) == 0 {
		t.Fatal("no routes found in server.go")
	}
	for r := range served {
		if !documented[r] {
			t.Errorf("route %s is served but not in openapi.yaml", r)
		}
	}
	for r := range documented {
		if !served[r] {
			t.Errorf("route %s is documented but not served", r)
		}
	}
	refRe := regexp.MustCompile(`\$ref: \./schemas/([a-z]+\.schema\.json#/\$defs/[A-Za-z]+)`)
	refs := refRe.FindAllStringSubmatch(string(b), -1)
	if len(refs) == 0 {
		t.Fatal("openapi.yaml references no schemas")
	}
	for _, m := range refs {
		if _, err := tolerant.schema(m[1], false); err != nil {
			t.Errorf("openapi $ref %s: %v", m[1], err)
		}
	}
}

// TestIPCRoutesMatchServer keeps the IPC route catalog complete.
func TestIPCRoutesMatchServer(t *testing.T) {
	doc, err := SchemaDoc("ipc.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	cat := doc["$defs"].(map[string]any)["IpcRoutes"].(map[string]any)["properties"].(map[string]any)
	re := regexp.MustCompile(`HandleFunc\("(/ipc/v1/[^"]+)"`)
	served := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(readSource(t, "internal/transport/ipc/ipc.go"), -1) {
		served[m[1]] = true
		if _, ok := cat[m[1]]; !ok {
			t.Errorf("IPC route %s missing from ipc.schema.json IpcRoutes", m[1])
		}
	}
	for r := range cat {
		if !served[r] {
			t.Errorf("IPC route %s is in the schema but not served", r)
		}
	}
}

// TestTypeScriptUpToDate fails when the schemas changed without regenerating
// the TypeScript module. Regenerate with `go test ./api -run TestTypeScriptUpToDate -update`.
func TestTypeScriptUpToDate(t *testing.T) {
	got, err := GenerateTS()
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile(TSFile, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(TSFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale; run `go test ./api -run TestTypeScriptUpToDate -update`", TSFile)
	}
}

func TestCurrentConfigContract(t *testing.T) {
	cfg := config.Default()
	cfg.Git.WorktreeRetentionHours = 24
	cfg.GitHub.Repository = "example/repo"
	cfg.GitHub.CloseIssues = true
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateStrict("config.schema.json", b); err != nil {
		t.Fatalf("config contract: %v\n%s", err, b)
	}
}

func TestGitHubOpenAPIResponseSchemas(t *testing.T) {
	b, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Content map[string]struct {
					Schema map[string]any `yaml:"schema"`
				} `yaml:"content"`
			} `yaml:"responses"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, method, status, ref string }{
		{"/v1/projects/{project_id}/github-history", "get", "200", "./schemas/entities.schema.json#/$defs/GitHubHistory"},
		{"/v1/projects/{project_id}/refresh", "post", "202", "./schemas/entities.schema.json#/$defs/RefreshProjectResult"},
	} {
		got := doc.Paths[tc.path][tc.method].Responses[tc.status].Content["application/json"].Schema["$ref"]
		if got != tc.ref {
			t.Errorf("%s %s response schema = %v, want %s", tc.method, tc.path, got, tc.ref)
		}
	}
}

func TestIPCNewSchemaFieldsMatchGo(t *testing.T) {
	doc, err := SchemaDoc("ipc.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	defs := doc["$defs"].(map[string]any)
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"PlanReviseRequest", plan.Revision{}},
		{"PlanReviseResult", app.PlanReviseResult{}},
		{"CandidateRef", struct {
			CandidateID string `json:"candidate_id"`
		}{}},
		{"PrioritizeRequest", struct {
			CandidateID string `json:"candidate_id"`
			app.PrioritizeRequest
		}{}},
		{"CleanupRequest", app.CleanupRequest{}},
		{"CleanupResult", app.CleanupResult{}},
		{"CleanupItem", app.CleanupItem{}},
		{"CommandResult", app.CommandResult{}},
		{"MetricsView", app.MetricsView{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := map[string]bool{}
			var fields func(reflect.Type)
			fields = func(typ reflect.Type) {
				for i := 0; i < typ.NumField(); i++ {
					f := typ.Field(i)
					if f.Anonymous {
						fields(f.Type)
						continue
					}
					tag := strings.Split(f.Tag.Get("json"), ",")[0]
					if tag != "" && tag != "-" {
						want[tag] = true
					}
				}
			}
			fields(reflect.TypeOf(tc.value))
			props := defs[tc.name].(map[string]any)["properties"].(map[string]any)
			for name := range want {
				if _, ok := props[name]; !ok {
					t.Errorf("schema missing Go field %s", name)
				}
			}
			for name := range props {
				if !want[name] {
					t.Errorf("schema field %s absent from Go", name)
				}
			}
		})
	}
}

func TestIPCRouteCatalogRequiresEveryRoute(t *testing.T) {
	doc, err := SchemaDoc("ipc.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	catalog := doc["$defs"].(map[string]any)["IpcRoutes"].(map[string]any)
	props := catalog["properties"].(map[string]any)
	required := map[string]bool{}
	for _, name := range catalog["required"].([]any) {
		required[name.(string)] = true
	}
	for name := range props {
		if !required[name] {
			t.Errorf("route %s is not required by the catalog schema", name)
		}
	}
	for name := range required {
		if _, ok := props[name]; !ok {
			t.Errorf("required route %s has no definition", name)
		}
	}
	err = ValidateStrict(Ref("ipc.schema.json", "IpcRoutes"), []byte(`{}`))
	if err == nil {
		t.Fatal("empty IPC route catalog accepted")
	}
	for _, name := range []string{"/ipc/v1/plans/revise", "/ipc/v1/merge/resolve", "/ipc/v1/merge/prioritize", "/ipc/v1/worktrees/cleanup"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("missing %s did not cause validation failure: %v", name, err)
		}
	}
}
