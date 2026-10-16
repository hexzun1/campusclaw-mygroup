package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// ErrDuplicateName reports a name that already exists inside the same class.
// Callers translate it into a 400.
var ErrDuplicateName = errors.New("duplicate name")

// ErrNoEnabledAgents reports a class in which no agent is enabled. Creating a
// conversation then is a 400, not a 404: nothing the caller specified is
// wrong, the class simply has no usable agent.
var ErrNoEnabledAgents = errors.New("no enabled agents")

// TutorAgent is one tutor agent row plus its current bindings. SkillIDs and
// MaterialIDs are always class-local: the binding write path validates that
// every id belongs to the agent's class.
type TutorAgent struct {
	ID          int
	ClassID     int
	Name        string
	Prompt      string
	Enabled     bool
	SkillIDs    []int
	MaterialIDs []int
	// SeedKey is set only on seeded agents; it makes seeding idempotent without
	// ever overwriting teacher edits. User-created agents leave it empty.
	SeedKey string
}

// Skill is one parsed skill file plus its row identity. SubjectText is the
// optional 资料 range; ToolsJSON is a JSON array (`[]` or
// `["knowledge_search"]`); for content stored by ParseSkill it is never NULL.
type Skill struct {
	ID          int
	ClassID     int
	Name        string
	TriggerText string
	SubjectText string
	ToolsJSON   []byte
	Description string
	SourceText  string
	Enabled     bool
	SeedKey     string
}

// Conversation is one conversation row. It is scoped by class and user at
// every read: both come from the caller's session, never from a request.
type Conversation struct {
	ID        int
	ClassID   int
	UserID    int
	AgentID   int
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Message is one persisted turn. Role is "user" or "assistant" (the database
// enforces the set); CitationsJSON is nil when a message carries no citations;
// SkillName is empty when a turn matched no skill.
type Message struct {
	ID             int
	ConversationID int
	Role           string
	Content        string
	CitationsJSON  []byte
	SkillName      string
	CreatedAt      time.Time
}

// ---------------------------------------------------------------------------
// Agents
// ---------------------------------------------------------------------------

// agentColumns is the projection every agent read shares.
const agentColumns = `id, class_id, name, prompt, enabled`

func scanAgent(row interface{ Scan(...any) error }) (*TutorAgent, error) {
	var a TutorAgent
	if err := row.Scan(&a.ID, &a.ClassID, &a.Name, &a.Prompt, &a.Enabled); err != nil {
		return nil, err
	}
	return &a, nil
}

// ListAgents returns the class's agents ordered by id. With enabledOnly the
// query itself excludes disabled agents, which is what students and
// conversation creation must see.
func ListAgents(ctx context.Context, conn *sql.DB, classID int, enabledOnly bool) ([]TutorAgent, error) {
	query := `SELECT ` + agentColumns + ` FROM tutor_agents WHERE class_id = ?`
	if enabledOnly {
		query += " AND enabled = 1"
	}
	query += " ORDER BY id"

	rows, err := conn.QueryContext(ctx, query, classID)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	var out []TutorAgent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := loadAgentBindings(ctx, conn, classID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetAgent returns one agent with its bindings. ErrNotFound covers missing,
// wrong class and — with enabledOnly — disabled: callers must not be able to
// tell those apart.
func GetAgent(ctx context.Context, conn *sql.DB, id, classID int, enabledOnly bool) (*TutorAgent, error) {
	query := `SELECT ` + agentColumns + ` FROM tutor_agents WHERE id = ? AND class_id = ?`
	if enabledOnly {
		query += " AND enabled = 1"
	}

	a, err := scanAgent(conn.QueryRowContext(ctx, query, id, classID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get agent: %w", err)
	}

	agents := []TutorAgent{*a}
	if err := loadAgentBindings(ctx, conn, classID, agents); err != nil {
		return nil, err
	}
	return &agents[0], nil
}

// GetAgentBySeedKey returns the seeded agent for a class, or ErrNotFound.
func GetAgentBySeedKey(ctx context.Context, conn *sql.DB, classID int, seedKey string) (*TutorAgent, error) {
	a, err := scanAgent(conn.QueryRowContext(ctx,
		`SELECT `+agentColumns+` FROM tutor_agents WHERE class_id = ? AND seed_key = ?`, classID, seedKey))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get agent by seed key: %w", err)
	}
	return a, nil
}

// CountEnabledAgents reports how many enabled agents the class has. The
// conversation-creation rule needs it to answer 400 before judging the
// specific agent id.
func CountEnabledAgents(ctx context.Context, conn *sql.DB, classID int) (int, error) {
	var n int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tutor_agents WHERE class_id = ? AND enabled = 1`, classID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count enabled agents: %w", err)
	}
	return n, nil
}

// loadAgentBindings fills SkillIDs and MaterialIDs for the given agents in two
// class-scoped queries and attaches them to the matching entries.
func loadAgentBindings(ctx context.Context, conn *sql.DB, classID int, agents []TutorAgent) error {
	if len(agents) == 0 {
		return nil
	}

	byID := make(map[int]*TutorAgent, len(agents))
	for i := range agents {
		byID[agents[i].ID] = &agents[i]
	}

	skillRows, err := conn.QueryContext(ctx, `
		SELECT b.agent_id, b.skill_id
		FROM tutor_agent_skills b
		JOIN tutor_agents a ON a.id = b.agent_id
		WHERE a.class_id = ?
		ORDER BY b.agent_id, b.skill_id`, classID)
	if err != nil {
		return fmt.Errorf("load agent skills: %w", err)
	}
	defer skillRows.Close()
	for skillRows.Next() {
		var agentID, skillID int
		if err := skillRows.Scan(&agentID, &skillID); err != nil {
			return fmt.Errorf("scan agent skill: %w", err)
		}
		if a := byID[agentID]; a != nil {
			a.SkillIDs = append(a.SkillIDs, skillID)
		}
	}
	if err := skillRows.Err(); err != nil {
		return err
	}

	materialRows, err := conn.QueryContext(ctx, `
		SELECT b.agent_id, b.material_id
		FROM tutor_agent_materials b
		JOIN tutor_agents a ON a.id = b.agent_id
		WHERE a.class_id = ?
		ORDER BY b.agent_id, b.material_id`, classID)
	if err != nil {
		return fmt.Errorf("load agent materials: %w", err)
	}
	defer materialRows.Close()
	for materialRows.Next() {
		var agentID, materialID int
		if err := materialRows.Scan(&agentID, &materialID); err != nil {
			return fmt.Errorf("scan agent material: %w", err)
		}
		if a := byID[agentID]; a != nil {
			a.MaterialIDs = append(a.MaterialIDs, materialID)
		}
	}
	return materialRows.Err()
}

// CreateAgent inserts an agent and its bindings in one transaction. The
// bindings are validated (same class, existing) before anything is written;
// any failure rolls the whole agent back.
func CreateAgent(ctx context.Context, conn *sql.DB, a TutorAgent) (int, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if dup, err := agentNameTaken(ctx, tx, a.ClassID, a.Name, 0); err != nil {
		return 0, err
	} else if dup {
		return 0, ErrDuplicateName
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO tutor_agents (class_id, name, prompt, enabled, seed_key) VALUES (?, ?, ?, ?, ?)`,
		a.ClassID, a.Name, a.Prompt, a.Enabled, nullableString(a.SeedKey))
	if err != nil {
		if isDuplicateKeyErr(err) {
			return 0, ErrDuplicateName
		}
		return 0, fmt.Errorf("insert agent: %w", err)
	}
	id64, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("agent last insert id: %w", err)
	}
	agentID := int(id64)

	if err := replaceAgentBindings(ctx, tx, agentID, a.ClassID, a.SkillIDs, a.MaterialIDs); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit tx: %w", err)
	}
	return agentID, nil
}

// UpdateAgent replaces an agent's fields and bindings in one transaction; an
// omitted binding list means an empty one. ErrNotFound covers both a missing
// agent and one in another class.
func UpdateAgent(ctx context.Context, conn *sql.DB, a TutorAgent) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var existing int
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM tutor_agents WHERE id = ? AND class_id = ? FOR UPDATE`, a.ID, a.ClassID).Scan(&existing); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock agent: %w", err)
	}

	if dup, err := agentNameTaken(ctx, tx, a.ClassID, a.Name, a.ID); err != nil {
		return err
	} else if dup {
		return ErrDuplicateName
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE tutor_agents SET name = ?, prompt = ?, enabled = ? WHERE id = ? AND class_id = ?`,
		a.Name, a.Prompt, a.Enabled, a.ID, a.ClassID); err != nil {
		if isDuplicateKeyErr(err) {
			return ErrDuplicateName
		}
		return fmt.Errorf("update agent: %w", err)
	}

	if err := replaceAgentBindings(ctx, tx, a.ID, a.ClassID, a.SkillIDs, a.MaterialIDs); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func agentNameTaken(ctx context.Context, tx *sql.Tx, classID int, name string, exceptID int) (bool, error) {
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tutor_agents WHERE class_id = ? AND name = ? AND id <> ?`,
		classID, name, exceptID).Scan(&n); err != nil {
		return false, fmt.Errorf("check agent name: %w", err)
	}
	return n > 0, nil
}

// replaceAgentBindings validates and rewrites both binding sets of an agent.
// Every id must exist in the agent's class; otherwise ErrNotFound and the
// transaction (including prior agent updates) was already rolled back by the
// caller. Duplicate ids are folded; the ids are written in the given order.
func replaceAgentBindings(ctx context.Context, tx *sql.Tx, agentID, classID int, skillIDs, materialIDs []int) error {
	skillIDs = dedupeIDs(skillIDs)
	materialIDs = dedupeIDs(materialIDs)

	if err := validateBindingIDs(ctx, tx, "skills", classID, skillIDs); err != nil {
		return err
	}
	if err := validateBindingIDs(ctx, tx, "materials", classID, materialIDs); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM tutor_agent_skills WHERE agent_id = ?`, agentID); err != nil {
		return fmt.Errorf("clear agent skills: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tutor_agent_materials WHERE agent_id = ?`, agentID); err != nil {
		return fmt.Errorf("clear agent materials: %w", err)
	}

	for _, skillID := range skillIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tutor_agent_skills (agent_id, skill_id) VALUES (?, ?)`, agentID, skillID); err != nil {
			return fmt.Errorf("bind skill: %w", err)
		}
	}
	for _, materialID := range materialIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tutor_agent_materials (agent_id, material_id) VALUES (?, ?)`, agentID, materialID); err != nil {
			return fmt.Errorf("bind material: %w", err)
		}
	}
	return nil
}

// validateBindingIDs returns ErrNotFound unless every id exists in the class.
// table is an internal constant, never user input.
func validateBindingIDs(ctx context.Context, tx *sql.Tx, table string, classID int, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, classID)
	for _, id := range ids {
		args = append(args, id)
	}

	var n int
	query := `SELECT COUNT(*) FROM ` + table + ` WHERE class_id = ? AND id IN (` + placeholders(len(ids)) + `)`
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return fmt.Errorf("validate %s binding: %w", table, err)
	}
	if n != len(ids) {
		return ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------------------
// Skills
// ---------------------------------------------------------------------------

const skillColumns = `id, class_id, name, trigger_text, subject_text, tools_json, description, source_text, enabled`

func scanSkill(row interface{ Scan(...any) error }) (*Skill, error) {
	var s Skill
	var subject sql.NullString
	if err := row.Scan(&s.ID, &s.ClassID, &s.Name, &s.TriggerText, &subject, &s.ToolsJSON,
		&s.Description, &s.SourceText, &s.Enabled); err != nil {
		return nil, err
	}
	s.SubjectText = subject.String
	return &s, nil
}

// ListSkills returns the class's skills ordered by id. With enabledOnly the
// query excludes disabled skills (the student view).
func ListSkills(ctx context.Context, conn *sql.DB, classID int, enabledOnly bool) ([]Skill, error) {
	query := `SELECT ` + skillColumns + ` FROM skills WHERE class_id = ?`
	if enabledOnly {
		query += " AND enabled = 1"
	}
	query += " ORDER BY id"

	rows, err := conn.QueryContext(ctx, query, classID)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	defer rows.Close()

	var out []Skill
	for rows.Next() {
		s, err := scanSkill(rows)
		if err != nil {
			return nil, fmt.Errorf("scan skill: %w", err)
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// GetSkill returns one skill; ErrNotFound covers missing and wrong class.
func GetSkill(ctx context.Context, conn *sql.DB, id, classID int) (*Skill, error) {
	s, err := scanSkill(conn.QueryRowContext(ctx,
		`SELECT `+skillColumns+` FROM skills WHERE id = ? AND class_id = ?`, id, classID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get skill: %w", err)
	}
	return s, nil
}

// GetSkillBySeedKey returns the seeded skill for a class, or ErrNotFound.
func GetSkillBySeedKey(ctx context.Context, conn *sql.DB, classID int, seedKey string) (*Skill, error) {
	s, err := scanSkill(conn.QueryRowContext(ctx,
		`SELECT `+skillColumns+` FROM skills WHERE class_id = ? AND seed_key = ?`, classID, seedKey))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get skill by seed key: %w", err)
	}
	return s, nil
}

// ListAgentSkillsEnabled returns the enabled skills currently bound to an
// agent, ordered by skill id. It is the candidate set routed on every
// question, so a changed binding or enabled flag takes effect immediately.
func ListAgentSkillsEnabled(ctx context.Context, conn *sql.DB, agentID, classID int) ([]Skill, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT s.id, s.class_id, s.name, s.trigger_text, s.subject_text, s.tools_json, s.description, s.source_text, s.enabled
		FROM skills s
		JOIN tutor_agent_skills b ON b.skill_id = s.id
		WHERE b.agent_id = ? AND s.class_id = ? AND s.enabled = 1
		ORDER BY s.id`, agentID, classID)
	if err != nil {
		return nil, fmt.Errorf("list agent skills: %w", err)
	}
	defer rows.Close()

	var out []Skill
	for rows.Next() {
		s, err := scanSkill(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent skill: %w", err)
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// CreateSkill inserts a skill after a same-class name check. ErrDuplicateName
// maps to 400.
func CreateSkill(ctx context.Context, conn *sql.DB, s Skill) (int, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if dup, err := skillNameTaken(ctx, tx, s.ClassID, s.Name, 0); err != nil {
		return 0, err
	} else if dup {
		return 0, ErrDuplicateName
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO skills (class_id, name, trigger_text, subject_text, tools_json, description, source_text, enabled, seed_key)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ClassID, s.Name, s.TriggerText, nullableString(s.SubjectText), s.ToolsJSON,
		s.Description, s.SourceText, s.Enabled, nullableString(s.SeedKey))
	if err != nil {
		if isDuplicateKeyErr(err) {
			return 0, ErrDuplicateName
		}
		return 0, fmt.Errorf("insert skill: %w", err)
	}
	id64, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("skill last insert id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit tx: %w", err)
	}
	return int(id64), nil
}

// UpdateSkill replaces a skill's content (but not its seed key) after a
// same-class existence check; ErrNotFound covers missing and wrong class.
func UpdateSkill(ctx context.Context, conn *sql.DB, s Skill) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var existing int
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM skills WHERE id = ? AND class_id = ? FOR UPDATE`, s.ID, s.ClassID).Scan(&existing); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock skill: %w", err)
	}

	if dup, err := skillNameTaken(ctx, tx, s.ClassID, s.Name, s.ID); err != nil {
		return err
	} else if dup {
		return ErrDuplicateName
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE skills SET name = ?, trigger_text = ?, subject_text = ?, tools_json = ?, description = ?, source_text = ?, enabled = ?
		 WHERE id = ? AND class_id = ?`,
		s.Name, s.TriggerText, nullableString(s.SubjectText), s.ToolsJSON, s.Description,
		s.SourceText, s.Enabled, s.ID, s.ClassID); err != nil {
		if isDuplicateKeyErr(err) {
			return ErrDuplicateName
		}
		return fmt.Errorf("update skill: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// DeleteSkill removes a skill; its agent bindings cascade. ErrNotFound covers
// missing and wrong class.
func DeleteSkill(ctx context.Context, conn *sql.DB, id, classID int) error {
	res, err := conn.ExecContext(ctx, `DELETE FROM skills WHERE id = ? AND class_id = ?`, id, classID)
	if err != nil {
		return fmt.Errorf("delete skill: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete skill rows: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func skillNameTaken(ctx context.Context, tx *sql.Tx, classID int, name string, exceptID int) (bool, error) {
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM skills WHERE class_id = ? AND name = ? AND id <> ?`,
		classID, name, exceptID).Scan(&n); err != nil {
		return false, fmt.Errorf("check skill name: %w", err)
	}
	return n > 0, nil
}

// ---------------------------------------------------------------------------
// Conversations and messages
// ---------------------------------------------------------------------------

const conversationColumns = `id, class_id, user_id, agent_id, title, created_at, updated_at`

// CreateConversation inserts a conversation titled 新的提问 for the given
// user, class and agent. The caller validates the agent first; the SQL here
// still scopes nothing else because all three ids come from the session and
// the validated agent.
func CreateConversation(ctx context.Context, conn *sql.DB, classID, userID, agentID int) (int, error) {
	res, err := conn.ExecContext(ctx,
		`INSERT INTO conversations (class_id, user_id, agent_id, title) VALUES (?, ?, ?, ?)`,
		classID, userID, agentID, "新的提问")
	if err != nil {
		return 0, fmt.Errorf("insert conversation: %w", err)
	}
	id64, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("conversation last insert id: %w", err)
	}
	return int(id64), nil
}

// ListConversations returns the user's own conversations in the class, most
// recently updated first. The user_id and class_id predicates are the
// isolation boundary; id breaks updated_at ties.
func ListConversations(ctx context.Context, conn *sql.DB, classID, userID int) ([]Conversation, error) {
	rows, err := conn.QueryContext(ctx,
		`SELECT `+conversationColumns+` FROM conversations
		 WHERE class_id = ? AND user_id = ?
		 ORDER BY updated_at DESC, id DESC`, classID, userID)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.ClassID, &c.UserID, &c.AgentID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetConversation returns one conversation of this user in this class.
// ErrNotFound covers another user's conversation, another class's and a
// missing one alike.
func GetConversation(ctx context.Context, conn *sql.DB, id, classID, userID int) (*Conversation, error) {
	var c Conversation
	err := conn.QueryRowContext(ctx,
		`SELECT `+conversationColumns+` FROM conversations WHERE id = ? AND class_id = ? AND user_id = ?`,
		id, classID, userID).Scan(&c.ID, &c.ClassID, &c.UserID, &c.AgentID, &c.Title, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get conversation: %w", err)
	}
	return &c, nil
}

// ListMessages returns every message of a conversation in order. The caller
// reaches this only after GetConversation proved the conversation belongs to
// the session, so no extra scope predicate is needed here.
func ListMessages(ctx context.Context, conn *sql.DB, conversationID int) ([]Message, error) {
	rows, err := conn.QueryContext(ctx,
		`SELECT id, conversation_id, role, content, citations_json, skill_name, created_at
		 FROM messages WHERE conversation_id = ? ORDER BY id`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// ListRecentMessages returns the conversation's last `limit` messages in
// ascending id order (the history window).
func ListRecentMessages(ctx context.Context, conn *sql.DB, conversationID, limit int) ([]Message, error) {
	rows, err := conn.QueryContext(ctx,
		`SELECT id, conversation_id, role, content, citations_json, skill_name, created_at FROM (
		   SELECT id, conversation_id, role, content, citations_json, skill_name, created_at
		   FROM messages WHERE conversation_id = ? ORDER BY id DESC LIMIT ?
		 ) AS recent ORDER BY id`, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent messages: %w", err)
	}
	defer rows.Close()

	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// AppendQuestion persists the question before any retrieval happens and
// refreshes the conversation's updated_at. On the first turn it also sets the
// title to the first 24 characters of the question (spec: 会话与消息).
func AppendQuestion(ctx context.Context, conn *sql.DB, conversationID int, question string) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var count int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE conversation_id = ?`, conversationID).Scan(&count); err != nil {
		return fmt.Errorf("count messages: %w", err)
	}

	if count == 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE conversations SET title = ?, updated_at = NOW() WHERE id = ?`,
			titleFromQuestion(question), conversationID); err != nil {
			return fmt.Errorf("set conversation title: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx,
			`UPDATE conversations SET updated_at = NOW() WHERE id = ?`, conversationID); err != nil {
			return fmt.Errorf("refresh conversation: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO messages (conversation_id, role, content) VALUES (?, 'user', ?)`,
		conversationID, question); err != nil {
		return fmt.Errorf("insert question: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// InsertAssistantMessage persists one assistant turn. citationsJSON is the
// citation array exactly as sent in `meta` (nil stores NULL) and skillName is
// empty when the turn matched no skill.
func InsertAssistantMessage(ctx context.Context, conn *sql.DB, conversationID int, content string, citationsJSON []byte, skillName string) (int, error) {
	res, err := conn.ExecContext(ctx,
		`INSERT INTO messages (conversation_id, role, content, citations_json, skill_name) VALUES (?, 'assistant', ?, ?, ?)`,
		conversationID, content, nullableJSON(citationsJSON), nullableString(skillName))
	if err != nil {
		return 0, fmt.Errorf("insert assistant message: %w", err)
	}
	id64, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("message last insert id: %w", err)
	}
	return int(id64), nil
}

func scanMessage(row interface{ Scan(...any) error }) (*Message, error) {
	var m Message
	var citations []byte
	var skillName sql.NullString
	if err := row.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &citations, &skillName, &m.CreatedAt); err != nil {
		return nil, fmt.Errorf("scan message: %w", err)
	}
	m.CitationsJSON = citations
	m.SkillName = skillName.String
	return &m, nil
}

// titleFromQuestion is the first 24 characters of the trimmed question.
func titleFromQuestion(question string) string {
	runes := []rune(strings.TrimSpace(question))
	if len(runes) > 24 {
		runes = runes[:24]
	}
	return string(runes)
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

// dedupeIDs folds duplicates while keeping the first occurrence order.
func dedupeIDs(ids []int) []int {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[int]bool, len(ids))
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// nullableString stores an empty string as SQL NULL.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// isDuplicateKeyErr reports MySQL error 1062 (duplicate entry), which the
// unique keys raise when a pre-check raced.
func isDuplicateKeyErr(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
