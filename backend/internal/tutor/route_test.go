package tutor

import (
	"testing"

	"campusclaw/backend/internal/db"
)

func skill(id int, name, trigger string) db.Skill {
	return db.Skill{ID: id, Name: name, TriggerText: trigger}
}

func TestRouteSplitsOnEverySeparator(t *testing.T) {
	// 中英文逗号、顿号、分号、竖线 — every separator must yield words.
	tests := []struct {
		name    string
		trigger string
		detail  string
	}{
		{"full-width comma", "数学，方程", "方程"},
		{"half-width comma", "数学,方程", "方程"},
		{"enumeration comma", "数学、方程", "方程"},
		{"full-width semicolon", "数学；方程", "方程"},
		{"half-width semicolon", "数学;方程", "方程"},
		{"pipe", "数学|方程", "方程"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Route("这道"+tt.detail+"怎么解", []db.Skill{skill(1, "数学解题引导", tt.trigger)})
			if got == nil {
				t.Fatalf("Route(%q) selected nothing, want the skill", tt.trigger)
			}
		})
	}
}

func TestRouteTrimsWordsAndDropsEmptyOnes(t *testing.T) {
	// " ， , 数学  " yields the single word 数学: surrounding blanks are
	// trimmed and the empty runs are dropped.
	got := Route("数学作业", []db.Skill{skill(1, "数学解题引导", " ， , 数学  ")})
	if got == nil || got.ID != 1 {
		t.Fatalf("Route = %v, want the skill (trimmed word)", got)
	}

	// A trigger of only separators has no word and can never match.
	if got := Route("数学", []db.Skill{skill(1, "空", "，，、；|")}); got != nil {
		t.Errorf("Route = %v, want nothing for a wordless trigger", got)
	}
}

func TestRouteIsCaseInsensitive(t *testing.T) {
	got := Route("my MATH homework", []db.Skill{skill(5, "数学解题引导", "Math|几何；代数")})
	if got == nil || got.ID != 5 {
		t.Fatalf("Route = %v, want the skill (word Math vs MATH)", got)
	}
}

func TestRoutePicksTheHighestWordCount(t *testing.T) {
	// 语文技能命中「作文」1 分；数学技能命中「方程」「计算」2 分。
	math := skill(7, "数学解题引导", "数学，方程，计算")
	chinese := skill(3, "语文解题引导", "语文，作文，阅读")

	got := Route("这道方程的计算哪里错了", []db.Skill{chinese, math})
	if got == nil || got.ID != 7 {
		t.Fatalf("Route = %v, want the mathematical skill (score 2)", got)
	}
}

func TestRouteTieBreaksBySmallerID(t *testing.T) {
	// 两个技能各命中一个词：编号较小者胜出，与候选顺序无关。
	low := skill(4, "低编号", "方程")
	high := skill(9, "高编号", "计算")

	for _, candidates := range [][]db.Skill{{low, high}, {high, low}} {
		got := Route("方程和计算", candidates)
		if got == nil || got.ID != 4 {
			t.Fatalf("Route(%v) = %v, want skill 4", candidates, got)
		}
	}
}

func TestRouteSelectsNothingOnAllZeroScores(t *testing.T) {
	// 语文技能在候选里，但数学技能没绑定时不跨界路由：只有绑定集合内的技能
	// 参与打分，全零即不选。
	got := Route("一道语文阅读题", []db.Skill{skill(7, "数学解题引导", "数学，方程，计算")})
	if got != nil {
		t.Fatalf("Route = %v, want nothing", got)
	}

	if got := Route("提问", nil); got != nil {
		t.Fatalf("Route(nil candidates) = %v, want nothing", got)
	}
}

func TestRouteSelectsAtMostOne(t *testing.T) {
	got := Route("数学方程和语文作文", []db.Skill{
		skill(1, "数学解题引导", "数学，方程"),
		skill(2, "语文解题引导", "语文，作文"),
	})
	if got == nil || got.ID != 1 {
		t.Fatalf("Route = %v, want exactly one skill (id 1 on the tie)", got)
	}
}
