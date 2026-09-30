package knowledge

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseParamsDefaults(t *testing.T) {
	params, err := ParseParams(nil)
	if err != nil {
		t.Fatalf("ParseParams(nil) failed: %v", err)
	}

	want := ChunkParams{Strategy: StrategyAuto, ChunkSize: AutoSize, OverlapPercent: DefaultOverlapPercent}
	if params != want {
		t.Fatalf("params = %+v, want %+v", params, want)
	}
	if size, overlap := params.Window(); size != 800 || overlap != 80 {
		t.Fatalf("default window = %d/%d, want 800/80", size, overlap)
	}
}

func TestParseParamsAcceptsEveryStrategy(t *testing.T) {
	for _, tc := range []struct {
		raw  map[string]string
		want Strategy
	}{
		{map[string]string{ParamStrategy: "auto"}, StrategyAuto},
		{map[string]string{ParamStrategy: "custom"}, StrategyCustom},
		{map[string]string{ParamStrategy: "hierarchy"}, StrategyHierarchy},
		{map[string]string{}, StrategyAuto},
	} {
		params, err := ParseParams(tc.raw)
		if err != nil {
			t.Fatalf("ParseParams(%v) failed: %v", tc.raw, err)
		}
		if params.Strategy != tc.want {
			t.Fatalf("ParseParams(%v).Strategy = %q, want %q", tc.raw, params.Strategy, tc.want)
		}
	}
}

func TestParseParamsCustomFull(t *testing.T) {
	params, err := ParseParams(map[string]string{
		ParamStrategy:           "custom",
		ParamChunkSize:          "200",
		ParamOverlapPercent:     "25",
		ParamRemoveURL:          "true",
		ParamRemoveEmail:        "false",
		ParamCollapseWhitespace: "true",
	})
	if err != nil {
		t.Fatalf("ParseParams failed: %v", err)
	}

	want := ChunkParams{
		Strategy:           StrategyCustom,
		ChunkSize:          200,
		OverlapPercent:     25,
		RemoveURL:          true,
		RemoveEmail:        false,
		CollapseWhitespace: true,
	}
	if params != want {
		t.Fatalf("params = %+v, want %+v", params, want)
	}
	if size, overlap := params.Window(); size != 200 || overlap != 50 {
		t.Fatalf("custom window = %d/%d, want 200/50", size, overlap)
	}
}

func TestParseParamsCustomBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  map[string]string
	}{
		{"最小片长", map[string]string{ParamStrategy: "custom", ParamChunkSize: "100", ParamOverlapPercent: "0"}},
		{"最大片长", map[string]string{ParamStrategy: "custom", ParamChunkSize: "2000", ParamOverlapPercent: "50"}},
		{"custom 不带片长", map[string]string{ParamStrategy: "custom"}},
		{"custom 只给重叠", map[string]string{ParamStrategy: "custom", ParamOverlapPercent: "30"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseParams(tc.raw); err != nil {
				t.Fatalf("ParseParams(%v) failed: %v", tc.raw, err)
			}
		})
	}
}

// TestParseParamsRejections is the table from tasks.md 3.2: chunk size 50,
// overlap 60, an unknown strategy and a non-boolean switch must all fail, and
// each message must name the offending parameter.
func TestParseParamsRejections(t *testing.T) {
	for _, tc := range []struct {
		name        string
		raw         map[string]string
		wantMessage string
	}{
		{"片长 50", map[string]string{ParamStrategy: "custom", ParamChunkSize: "50"}, ParamChunkSize},
		{"片长 99", map[string]string{ParamStrategy: "custom", ParamChunkSize: "99"}, ParamChunkSize},
		{"片长 2001", map[string]string{ParamStrategy: "custom", ParamChunkSize: "2001"}, ParamChunkSize},
		{"片长非数字", map[string]string{ParamStrategy: "custom", ParamChunkSize: "abc"}, ParamChunkSize},
		{"片长为空", map[string]string{ParamStrategy: "custom", ParamChunkSize: ""}, ParamChunkSize},
		{"重叠 60", map[string]string{ParamStrategy: "custom", ParamOverlapPercent: "60"}, ParamOverlapPercent},
		{"重叠为负", map[string]string{ParamStrategy: "custom", ParamOverlapPercent: "-1"}, ParamOverlapPercent},
		{"重叠非数字", map[string]string{ParamStrategy: "custom", ParamOverlapPercent: "half"}, ParamOverlapPercent},
		{"未知策略", map[string]string{ParamStrategy: "sentence"}, ParamStrategy},
		{"空策略", map[string]string{ParamStrategy: ""}, ParamStrategy},
		{"开关为 maybe", map[string]string{ParamRemoveURL: "maybe"}, ParamRemoveURL},
		{"开关为 1", map[string]string{ParamRemoveEmail: "1"}, ParamRemoveEmail},
		{"开关为空", map[string]string{ParamCollapseWhitespace: ""}, ParamCollapseWhitespace},
		{"开关为 TRUE", map[string]string{ParamRemoveURL: "TRUE"}, ParamRemoveURL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseParams(tc.raw)
			if err == nil {
				t.Fatalf("ParseParams(%v) succeeded, want an error", tc.raw)
			}
			if !strings.Contains(err.Error(), tc.wantMessage) {
				t.Fatalf("error %q does not name %s", err, tc.wantMessage)
			}
		})
	}
}

// TestParseParamsRejectsWindowWithFixedStrategies pins the decision that auto
// and hierarchy have a fixed window: accepting chunk_size there would either
// contradict the spec or silently ignore the value.
func TestParseParamsRejectsWindowWithFixedStrategies(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  map[string]string
	}{
		{"auto 带片长", map[string]string{ParamStrategy: "auto", ParamChunkSize: "200"}},
		{"auto 带重叠", map[string]string{ParamStrategy: "auto", ParamOverlapPercent: "20"}},
		{"缺省策略带片长", map[string]string{ParamChunkSize: "200"}},
		{"hierarchy 带片长", map[string]string{ParamStrategy: "hierarchy", ParamChunkSize: "200"}},
		{"hierarchy 带重叠", map[string]string{ParamStrategy: "hierarchy", ParamOverlapPercent: "20"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseParams(tc.raw); err == nil {
				t.Fatalf("ParseParams(%v) succeeded, want an error", tc.raw)
			}
		})
	}
}

func TestParseParamsKeepsFixedWindowForNonCustomStrategies(t *testing.T) {
	params, err := ParseParams(map[string]string{ParamStrategy: "hierarchy", ParamCollapseWhitespace: "true"})
	if err != nil {
		t.Fatalf("ParseParams failed: %v", err)
	}
	if size, overlap := params.Window(); size != 800 || overlap != 80 {
		t.Fatalf("hierarchy window = %d/%d, want the fixed 800/80", size, overlap)
	}
}

func TestParamsJSONIsValidJSON(t *testing.T) {
	params, err := ParseParams(map[string]string{
		ParamStrategy:       "custom",
		ParamChunkSize:      "200",
		ParamOverlapPercent: "25",
		ParamRemoveURL:      "true",
	})
	if err != nil {
		t.Fatalf("ParseParams failed: %v", err)
	}

	var decoded struct {
		Strategy           string `json:"strategy"`
		ChunkSize          int    `json:"chunk_size"`
		OverlapPercent     int    `json:"overlap_percent"`
		RemoveURL          bool   `json:"remove_url"`
		RemoveEmail        bool   `json:"remove_email"`
		CollapseWhitespace bool   `json:"collapse_whitespace"`
	}
	if err := json.Unmarshal(params.ParamsJSON(), &decoded); err != nil {
		t.Fatalf("chunk_params is not valid JSON (%s): %v", params.ParamsJSON(), err)
	}
	if decoded.Strategy != "custom" || decoded.ChunkSize != 200 || decoded.OverlapPercent != 25 || !decoded.RemoveURL {
		t.Fatalf("round-tripped params = %+v", decoded)
	}
}
