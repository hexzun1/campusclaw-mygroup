package knowledge

import (
	"strings"
	"testing"
)

func TestParseParamsJSONDefaults(t *testing.T) {
	for _, body := range []string{"", "   ", "{}", "null"} {
		params, err := ParseParamsJSON([]byte(body))
		if err != nil {
			t.Fatalf("ParseParamsJSON(%q) failed: %v", body, err)
		}
		if params != DefaultParams() {
			t.Fatalf("ParseParamsJSON(%q) = %+v, want the defaults", body, params)
		}
	}
}

func TestParseParamsJSONAcceptsTypedValues(t *testing.T) {
	params, err := ParseParamsJSON([]byte(`{
		"strategy": "custom",
		"chunk_size": 200,
		"overlap_percent": 25,
		"remove_url": true,
		"remove_email": false,
		"collapse_whitespace": true
	}`))
	if err != nil {
		t.Fatalf("ParseParamsJSON failed: %v", err)
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
}

func TestParseParamsJSONAcceptsStringValues(t *testing.T) {
	params, err := ParseParamsJSON([]byte(`{"strategy":"hierarchy","collapse_whitespace":"true"}`))
	if err != nil {
		t.Fatalf("ParseParamsJSON failed: %v", err)
	}
	if params.Strategy != StrategyHierarchy || !params.CollapseWhitespace {
		t.Fatalf("params = %+v, want hierarchy with whitespace folding", params)
	}
}

func TestParseParamsJSONRejections(t *testing.T) {
	for _, tc := range []struct {
		name        string
		body        string
		wantMessage string
	}{
		{"片长 50", `{"strategy":"custom","chunk_size":50}`, ParamChunkSize},
		{"重叠 60", `{"strategy":"custom","overlap_percent":60}`, ParamOverlapPercent},
		{"未知策略", `{"strategy":"sentence"}`, ParamStrategy},
		{"开关为字符串 maybe", `{"remove_url":"maybe"}`, ParamRemoveURL},
		{"开关为数字", `{"remove_url":1}`, ParamRemoveURL},
		{"数组值", `{"remove_url":[true]}`, ParamRemoveURL},
		{"对象值", `{"chunk_size":{"n":100}}`, ParamChunkSize},
		{"body 不是对象", `[1,2,3]`, "JSON object"},
		{"auto 带片长", `{"strategy":"auto","chunk_size":200}`, ParamChunkSize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseParamsJSON([]byte(tc.body))
			if err == nil {
				t.Fatalf("ParseParamsJSON(%s) succeeded, want an error", tc.body)
			}
			if !strings.Contains(err.Error(), tc.wantMessage) {
				t.Fatalf("error %q does not name %s", err, tc.wantMessage)
			}
		})
	}
}

// TestParseJSONAndFormAgree checks that the two input shapes share one set of
// rules: the same parameter values must validate identically.
func TestParseJSONAndFormAgree(t *testing.T) {
	form := map[string]string{
		ParamStrategy:           "custom",
		ParamChunkSize:          "150",
		ParamOverlapPercent:     "30",
		ParamRemoveURL:          "true",
		ParamRemoveEmail:        "false",
		ParamCollapseWhitespace: "true",
	}
	fromForm, err := ParseParams(form)
	if err != nil {
		t.Fatalf("ParseParams failed: %v", err)
	}

	fromJSON, err := ParseParamsJSON([]byte(`{
		"strategy":"custom","chunk_size":150,"overlap_percent":30,
		"remove_url":true,"remove_email":false,"collapse_whitespace":true,
		"title":"ignored extra field"
	}`))
	if err != nil {
		t.Fatalf("ParseParamsJSON failed: %v", err)
	}

	if fromForm != fromJSON {
		t.Fatalf("form gave %+v, JSON gave %+v", fromForm, fromJSON)
	}
}
