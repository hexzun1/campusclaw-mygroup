package tutor

import (
	"strings"
	"testing"
)

// run pushes every fragment through one filter and collects the pieces that
// would become deltas, plus the final finish output.
func run(f *OutputFilter, fragments []string) (pushed []string, tail string, failed bool) {
	for _, frag := range fragments {
		if out := f.Feed(frag); out != "" {
			pushed = append(pushed, out)
		}
	}
	tail, failed = f.Finish()
	if tail != "" {
		pushed = append(pushed, tail)
	}
	return pushed, tail, failed
}

func joined(pushed []string) string { return strings.Join(pushed, "") }

func TestOutputFilterKeepsInRangeCitations(t *testing.T) {
	pushed, _, failed := run(NewOutputFilter(3), []string{"答案是 [1] 和 [3]，不是 [7]。"})
	if failed {
		t.Fatal("Finish reported a failure")
	}
	want := "答案是 [1] 和 [3]，不是 。"
	if got := joined(pushed); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestOutputFilterSplitMarkerAcrossFragments(t *testing.T) {
	// [12] 被拆成 "[1" 与 "2]"：拼接结果必须与整段到达时一致。
	whole, _, _ := run(NewOutputFilter(3), []string{"见 [3]"})
	split, _, _ := run(NewOutputFilter(3), []string{"见 [", "3", "]"})
	if joined(split) != joined(whole) || joined(whole) != "见 [3]" {
		t.Errorf("split = %q, whole = %q, want both 见 [3]", joined(split), joined(whole))
	}

	// 越界编号被拆开移除后，两段文字直接相邻。
	pushed, _, _ := run(NewOutputFilter(1), []string{"前[1", "2]后"})
	if got := joined(pushed); got != "前后" {
		t.Errorf("output = %q, want 前后", got)
	}
}

func TestOutputFilterZeroCitationsRemovesEverything(t *testing.T) {
	pushed, _, failed := run(NewOutputFilter(0), []string{"答案见[1]和[2]。"})
	if failed {
		t.Fatal("Finish reported a failure")
	}
	if got := joined(pushed); got != "答案见和。" {
		t.Errorf("output = %q, want 答案见和。", got)
	}
}

func TestOutputFilterNonMarkersPassThrough(t *testing.T) {
	tests := []struct {
		name      string
		fragments []string
		want      string
	}{
		{"letters", []string{"[a]"}, "[a]"},
		{"empty brackets", []string{"[]"}, "[]"},
		{"unclosed bracket at the end", []string{"前[1"}, "前[1"},
		{"bracket then new marker", []string{"[[1]"}, "[[1]"},
		{"spaced marker", []string{"[ 1 ]"}, "[ 1 ]"},
		{"zero is out of range", []string{"[0]"}, ""},
		{"minus one is ordinary text", []string{"[-1]"}, "[-1]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pushed, _, failed := run(NewOutputFilter(2), tt.fragments)
			if failed {
				t.Fatal("Finish reported a failure")
			}
			if got := joined(pushed); got != tt.want {
				t.Errorf("output = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOutputFilterHoldsThenReleasesTheBannedPrefix(t *testing.T) {
	// 逐个字符推送「资料中未找到相关内容不改」：在前缀期间输出为空，第一个
	// 偏离的字符落地时已暂存部分一次性释放。
	f := NewOutputFilter(0)
	var early []string
	for _, r := range "资料中未找到相关内容" {
		if out := f.Feed(string(r)); out != "" {
			early = append(early, out)
		}
	}
	if len(early) != 0 {
		t.Fatalf("prefix produced output early: %v", early)
	}

	rest := f.Feed("不改")
	tail, failed := f.Finish()
	if failed {
		t.Fatal("Finish reported a failure")
	}
	got := rest + tail
	want := "资料中未找到相关内容不改"
	if got != want {
		t.Errorf("released text = %q, want %q", got, want)
	}
}

func TestOutputFilterFailsOnExactlyTheBannedAnswer(t *testing.T) {
	// 整段等于禁用文案：报告失败，且从未推送任何文字。
	pushed, _, failed := run(NewOutputFilter(0), []string{"资料中未找到", "相关内容"})
	if !failed {
		t.Fatal("Finish must report a failure for the banned answer")
	}
	if got := joined(pushed); got != "" {
		t.Errorf("pushed %q, want nothing", got)
	}

	// 首尾空白不影响判定。
	_, _, failed = run(NewOutputFilter(0), []string{"\n 资料中未找到相关内容 \n"})
	if !failed {
		t.Error("leading/trailing whitespace must not defeat the check")
	}

	// 带一个被剔除的越界标注，剩余正文仍等于禁用文案：同样失败。
	_, _, failed = run(NewOutputFilter(0), []string{"资料中未找到相关内容[9]"})
	if !failed {
		t.Error("a dropped out-of-range marker must not defeat the check")
	}
}

func TestOutputFilterReleasesWhenTheBannedTextGetsAMarker(t *testing.T) {
	// 禁用文案后面跟着一个有效标注：整段不再等于禁用文案，照常回答。
	pushed, _, failed := run(NewOutputFilter(1), []string{"资料中未找到相关内容[1]"})
	if failed {
		t.Fatal("an in-range marker makes the answer legal")
	}
	if got := joined(pushed); got != "资料中未找到相关内容[1]" {
		t.Errorf("output = %q", got)
	}
}

func TestOutputFilterConcatenationMatchesExpectation(t *testing.T) {
	// 任意拆分都必须拼出同一段期望全文。
	want := "几何 [1] 与 [2]，没有 [5]。"
	for chunk := 1; chunk <= 6; chunk++ {
		var fragments []string
		runes := []rune(want)
		for i := 0; i < len(runes); i += chunk {
			end := i + chunk
			if end > len(runes) {
				end = len(runes)
			}
			fragments = append(fragments, string(runes[i:end]))
		}
		pushed, _, failed := run(NewOutputFilter(2), fragments)
		if failed {
			t.Fatalf("chunk %d: unexpected failure", chunk)
		}
		expect := "几何 [1] 与 [2]，没有 。"
		if got := joined(pushed); got != expect {
			t.Errorf("chunk %d: output = %q, want %q", chunk, got, expect)
		}
	}
}
