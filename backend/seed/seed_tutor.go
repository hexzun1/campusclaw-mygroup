package seed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/tutor"
)

// Seed keys make the tutor seeds idempotent without ever overwriting teacher
// edits: an existing (class_id, seed_key) row is left untouched, so a renamed
// or re-prompted default agent survives every restart (design.md Decision 11).
const (
	mathGuideSeedKey    = "math-guide"
	chineseGuideSeedKey = "chinese-guide"
	defaultAgentSeedKey = "default-agent"

	defaultAgentName = "默认助教"
	// defaultAgentPrompt is the pipeline's fallback role setting (design.md
	// Decision 5), seeded as an ordinary editable prompt.
	defaultAgentPrompt = "你是一名耐心、严谨的课程助教，面向本班学生答疑。"
)

// The two preset skill files are seeded through tutor.ParseSkill, exactly like
// a teacher submission, so SourceText stays a valid re-parseable file.
var (
	mathGuideSource = `名称：数学解题引导
适用条件：数学，方程，计算，函数，几何，应用题，解题，math
资料：数学
工具：knowledge_search
启用：是

引导学生自己解决数学问题。按以下步骤作答：
1. 先判断学生的思路或答案是否正确，指出对在哪里、错在哪一步；
2. 针对错误的步骤提出一个启发性的小问题，引导学生自己找到正确的下一步；
3. 只给出必要的知识点与方法提示，不给出最终答案、选项、得数与完整解题过程。
`

	chineseGuideSource = `名称：语文解题引导
适用条件：语文，阅读，作文，古诗，文言文，修辞，病句
资料：语文
工具：knowledge_search
启用：是

引导学生自己解决语文问题。按以下步骤作答：
1. 先判断学生的思路或答案是否正确，指出对在哪里、错在哪一步；
2. 针对错误之处提出一个启发性的小问题，引导学生回到原文找依据或调整思路；
3. 只给出阅读方法、修辞或文言知识方面的提示，不给出完整答案与成文范文。
`
)

// ensureTutorData idempotently creates the preset skills and one enabled
// default agent per class (spec: 预置技能与默认智能体). It runs after the
// material seeds so the first A-class agent creation can bind the materials
// that already exist at that moment; later material changes are the teacher's
// to manage.
//
// A preset skill deleted by a teacher is recreated on the next restart (an
// acknowledged side effect of seeding by key, design.md Decision 11); if the
// name is meanwhile taken by a teacher-made skill, the seed steps aside.
func ensureTutorData(ctx context.Context, conn *sql.DB, classAID, classBID int) error {
	mathSkillID, err := ensureSeedSkill(ctx, conn, classAID, mathGuideSeedKey, mathGuideSource)
	if err != nil {
		return fmt.Errorf("ensure math guide skill: %w", err)
	}
	chineseSkillID, err := ensureSeedSkill(ctx, conn, classAID, chineseGuideSeedKey, chineseGuideSource)
	if err != nil {
		return fmt.Errorf("ensure chinese guide skill: %w", err)
	}

	classAMaterials, err := db.ListMaterials(ctx, conn, classAID, "")
	if err != nil {
		return fmt.Errorf("list class A materials for default agent: %w", err)
	}
	materialIDs := make([]int, 0, len(classAMaterials))
	for _, m := range classAMaterials {
		materialIDs = append(materialIDs, m.ID)
	}

	if err := ensureSeedAgent(ctx, conn, classAID, []int{mathSkillID, chineseSkillID}, materialIDs); err != nil {
		return fmt.Errorf("ensure class A default agent: %w", err)
	}
	if err := ensureSeedAgent(ctx, conn, classBID, nil, nil); err != nil {
		return fmt.Errorf("ensure class B default agent: %w", err)
	}
	return nil
}

func ensureSeedSkill(ctx context.Context, conn *sql.DB, classID int, seedKey, source string) (int, error) {
	if existing, err := db.GetSkillBySeedKey(ctx, conn, classID, seedKey); err == nil {
		return existing.ID, nil
	} else if !errors.Is(err, db.ErrNotFound) {
		return 0, err
	}

	s, err := tutor.ParseSkill(source)
	if err != nil {
		return 0, fmt.Errorf("parse seed skill %s: %w", seedKey, err)
	}
	s.ClassID = classID
	s.SeedKey = seedKey

	// Seeded skills reference only knowledge_search, never an MCP server, so
	// there is nothing to lock.
	id, err := db.CreateSkill(ctx, conn, s, nil)
	if errors.Is(err, db.ErrDuplicateName) {
		log.Printf("seed: skill %q already exists in class %d outside the seed, keeping it", s.Name, classID)
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("insert seed skill %s: %w", seedKey, err)
	}
	log.Printf("seed: created skill %q for class %d", s.Name, classID)
	return id, nil
}

// ensureSeedAgent creates the class default agent only if no agent with the
// seed key exists yet. An existing agent keeps its current name, prompt,
// enabled flag and bindings, whatever the teacher made of them.
func ensureSeedAgent(ctx context.Context, conn *sql.DB, classID int, skillIDs, materialIDs []int) error {
	if _, err := db.GetAgentBySeedKey(ctx, conn, classID, defaultAgentSeedKey); err == nil {
		return nil
	} else if !errors.Is(err, db.ErrNotFound) {
		return err
	}

	if _, err := db.CreateAgent(ctx, conn, db.TutorAgent{
		ClassID:     classID,
		Name:        defaultAgentName,
		Prompt:      defaultAgentPrompt,
		Enabled:     true,
		SkillIDs:    skillIDs,
		MaterialIDs: materialIDs,
		SeedKey:     defaultAgentSeedKey,
	}); err != nil {
		return fmt.Errorf("insert default agent: %w", err)
	}
	log.Printf("seed: created default agent for class %d", classID)
	return nil
}
