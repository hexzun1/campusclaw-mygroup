package tutor

import (
	"strings"

	"campusclaw/backend/internal/db"
)

// triggerSeparators are the characters a skill's 适用条件 is split on: both
// comma kinds, the enumeration comma, both semicolon kinds and the pipe.
const triggerSeparators = "，,、；;|"

// Route picks at most one skill for the question (spec: 技能路由). Every
// candidate is scored — one point per trigger word that occurs in the
// question, compared case-insensitively — and the highest score wins; ties go
// to the smaller skill ID. An all-zero result selects nothing, so a question
// no bound skill knows about is answered without a skill.
//
// The candidates are read from the database on every question, so a changed
// binding, enabled flag or 适用条件 takes effect on the very next turn.
func Route(question string, candidates []db.Skill) *db.Skill {
	lowered := strings.ToLower(question)

	var best *db.Skill
	bestScore := 0
	for i := range candidates {
		score := triggerScore(lowered, candidates[i].TriggerText)
		if score == 0 {
			continue
		}
		if best == nil || score > bestScore || (score == bestScore && candidates[i].ID < best.ID) {
			best = &candidates[i]
			bestScore = score
		}
	}
	return best
}

// triggerScore counts how many of the trigger words occur in the lowered
// question. Words are trimmed and separated on any of the trigger separators;
// an empty word cannot be a substring of anything and is skipped.
func triggerScore(loweredQuestion, triggerText string) int {
	score := 0
	for _, word := range strings.FieldsFunc(triggerText, isTriggerSeparator) {
		word = strings.TrimSpace(word)
		if word == "" {
			continue
		}
		if strings.Contains(loweredQuestion, strings.ToLower(word)) {
			score++
		}
	}
	return score
}

func isTriggerSeparator(r rune) bool {
	return strings.ContainsRune(triggerSeparators, r)
}
