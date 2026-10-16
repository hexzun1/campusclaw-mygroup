package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// These tests need the same seeded MySQL as the other integration tests
// (CAMPUSCLAW_TEST_DSN) and additionally exercise the 003_tutor.sql tables.
// Every row a test creates is removed again.

func userIDByUsername(t *testing.T, conn *sql.DB, username string) int {
	t.Helper()
	var id int
	if err := conn.QueryRowContext(context.Background(),
		`SELECT id FROM users WHERE username = ?`, username).Scan(&id); err != nil {
		t.Skipf("seeded user %s missing (%v); is this a seeded database?", username, err)
	}
	return id
}

func createTestSkill(t *testing.T, conn *sql.DB, classID int, name string) int {
	t.Helper()
	id, err := CreateSkill(context.Background(), conn, Skill{
		ClassID:     classID,
		Name:        name,
		TriggerText: "测试，test",
		ToolsJSON:   []byte("[]"),
		Description: "测试用技能说明。",
		SourceText:  "名称：" + name,
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("insert test skill: %v", err)
	}
	t.Cleanup(func() {
		if _, err := conn.ExecContext(context.Background(), `DELETE FROM skills WHERE id = ?`, id); err != nil {
			t.Errorf("cleanup skill %d: %v", id, err)
		}
	})
	return id
}

func createTestAgent(t *testing.T, conn *sql.DB, classID int, name string, skillIDs, materialIDs []int) int {
	t.Helper()
	id, err := CreateAgent(context.Background(), conn, TutorAgent{
		ClassID:     classID,
		Name:        name,
		Prompt:      "测试用智能体。",
		Enabled:     true,
		SkillIDs:    skillIDs,
		MaterialIDs: materialIDs,
	})
	if err != nil {
		t.Fatalf("insert test agent: %v", err)
	}
	t.Cleanup(func() {
		// tutor_agent_skills / tutor_agent_materials cascade from the agent.
		if _, err := conn.ExecContext(context.Background(), `DELETE FROM tutor_agents WHERE id = ?`, id); err != nil {
			t.Errorf("cleanup agent %d: %v", id, err)
		}
	})
	return id
}

func createTestConversation(t *testing.T, conn *sql.DB, classID, userID, agentID int) int {
	t.Helper()
	id, err := CreateConversation(context.Background(), conn, classID, userID, agentID)
	if err != nil {
		t.Fatalf("insert test conversation: %v", err)
	}
	t.Cleanup(func() {
		// messages cascade from the conversation.
		if _, err := conn.ExecContext(context.Background(), `DELETE FROM conversations WHERE id = ?`, id); err != nil {
			t.Errorf("cleanup conversation %d: %v", id, err)
		}
	})
	return id
}

// TestTutorCrossClassReadsAreNotFound is the class-isolation guarantee of the
// tutor reads: an A-class identity must not reach B-class agents, skills or
// conversations, and the "wrong class" answer is the same ErrNotFound as
// "does not exist" (design.md Decision 4).
func TestTutorCrossClassReadsAreNotFound(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()

	classA, classB, teacherA := fixture(t, conn)
	studentB1 := userIDByUsername(t, conn, "student_b1")

	bSkill := createTestSkill(t, conn, classB, "测试技能-乙班")
	bAgent := createTestAgent(t, conn, classB, "测试智能体-乙班", nil, nil)
	bConv := createTestConversation(t, conn, classB, studentB1, bAgent)

	if _, err := GetAgent(ctx, conn, bAgent, classA, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetAgent(B-class id as A class) = %v, want ErrNotFound", err)
	}
	if _, err := GetSkill(ctx, conn, bSkill, classA); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSkill(B-class id as A class) = %v, want ErrNotFound", err)
	}
	if _, err := GetConversation(ctx, conn, bConv, classA, teacherA); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetConversation(B-class id as A class) = %v, want ErrNotFound", err)
	}

	// Positive control: the same reads succeed from the owning class.
	if _, err := GetAgent(ctx, conn, bAgent, classB, false); err != nil {
		t.Errorf("GetAgent(B-class id as B class): %v", err)
	}
	if _, err := GetSkill(ctx, conn, bSkill, classB); err != nil {
		t.Errorf("GetSkill(B-class id as B class): %v", err)
	}
}

// TestTutorConversationReadRequiresOwner: even inside the right class, a
// conversation is private to the user who created it (spec: 会话只属于本人).
func TestTutorConversationReadRequiresOwner(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()

	classA, _, teacherA := fixture(t, conn)
	studentA1 := userIDByUsername(t, conn, "student_a1")

	agent := createTestAgent(t, conn, classA, "测试智能体-归属", nil, nil)
	conv := createTestConversation(t, conn, classA, studentA1, agent)

	if _, err := GetConversation(ctx, conn, conv, classA, teacherA); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetConversation(other user's) = %v, want ErrNotFound", err)
	}
	if _, err := GetConversation(ctx, conn, conv, classA, studentA1); err != nil {
		t.Errorf("GetConversation(owner) = %v, want success", err)
	}
}

// TestTutorBindingValidationRollsBack: a binding set with a foreign-class or a
// nonexistent id is rejected as ErrNotFound, and nothing — neither the agent
// nor the previous bindings — changes (spec: 只能绑定本班资源).
func TestTutorBindingValidationRollsBack(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()

	classA, classB, _ := fixture(t, conn)

	bSkill := createTestSkill(t, conn, classB, "测试技能-跨班绑定")
	aSkill := createTestSkill(t, conn, classA, "测试技能-本班绑定")

	materialA, _ := createTestMaterial(t, conn, classA, userIDByUsername(t, conn, "teacher_a"), "测试材料-绑定", "绑定测试正文")

	// Creation with a foreign-class skill leaves no agent behind.
	if _, err := CreateAgent(ctx, conn, TutorAgent{
		ClassID: classA, Name: "测试智能体-非法技能", Enabled: true, SkillIDs: []int{bSkill},
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("CreateAgent(foreign skill) = %v, want ErrNotFound", err)
	}
	if n := agentCountByName(t, conn, classA, "测试智能体-非法技能"); n != 0 {
		t.Errorf("rolled-back agent rows = %d, want 0", n)
	}

	// Creation with a nonexistent material id is the same.
	if _, err := CreateAgent(ctx, conn, TutorAgent{
		ClassID: classA, Name: "测试智能体-不存在材料", Enabled: true, MaterialIDs: []int{999999},
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("CreateAgent(nonexistent material) = %v, want ErrNotFound", err)
	}
	if n := agentCountByName(t, conn, classA, "测试智能体-不存在材料"); n != 0 {
		t.Errorf("rolled-back agent rows = %d, want 0", n)
	}

	// Update with a bad binding keeps the previous fields and bindings.
	agent := createTestAgent(t, conn, classA, "测试智能体-回滚", []int{aSkill}, []int{materialA})

	err := UpdateAgent(ctx, conn, TutorAgent{
		ID: agent, ClassID: classA, Name: "测试智能体-回滚-改名", Prompt: "改名了", Enabled: false,
		SkillIDs: []int{bSkill}, MaterialIDs: []int{materialA},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateAgent(foreign skill) = %v, want ErrNotFound", err)
	}
	err = UpdateAgent(ctx, conn, TutorAgent{
		ID: agent, ClassID: classA, Name: "测试智能体-回滚-改名", Prompt: "改名了", Enabled: false,
		SkillIDs: []int{aSkill}, MaterialIDs: []int{999999},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateAgent(nonexistent material) = %v, want ErrNotFound", err)
	}

	got, err := GetAgent(ctx, conn, agent, classA, false)
	if err != nil {
		t.Fatalf("GetAgent after rollback: %v", err)
	}
	if got.Name != "测试智能体-回滚" || !got.Enabled || got.Prompt != "测试用智能体。" {
		t.Errorf("agent after rollback = %+v, want unchanged name/prompt/enabled", got)
	}
	if len(got.SkillIDs) != 1 || got.SkillIDs[0] != aSkill {
		t.Errorf("skill bindings after rollback = %v, want [%d]", got.SkillIDs, aSkill)
	}
	if len(got.MaterialIDs) != 1 || got.MaterialIDs[0] != materialA {
		t.Errorf("material bindings after rollback = %v, want [%d]", got.MaterialIDs, materialA)
	}
}

func agentCountByName(t *testing.T, conn *sql.DB, classID int, name string) int {
	t.Helper()
	var n int
	if err := conn.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM tutor_agents WHERE class_id = ? AND name = ?`, classID, name).Scan(&n); err != nil {
		t.Fatalf("count agents by name: %v", err)
	}
	return n
}
