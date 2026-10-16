-- 迭代 4：助教智能体、技能、绑定、会话与消息。
--
-- 与 002 相同的原因，本文件可能被执行两次：全新数据卷上 MySQL 镜像的
-- docker-entrypoint-initdb.d 会先跑一遍，随后 api 启动时的迁移执行器
-- （internal/db.Migrate）因为 schema_migrations 里没有记录而再跑一遍。
-- 因此这里全部写成可重复执行的：CREATE TABLE IF NOT EXISTS。

CREATE TABLE IF NOT EXISTS tutor_agents (
    id INT AUTO_INCREMENT PRIMARY KEY,
    class_id INT NOT NULL,
    name VARCHAR(64) NOT NULL,
    prompt TEXT NOT NULL,
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    seed_key VARCHAR(64) NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uq_agents_class_name (class_id, name),
    UNIQUE KEY uq_agents_class_seed (class_id, seed_key),
    CONSTRAINT fk_agents_class FOREIGN KEY (class_id) REFERENCES classes(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS skills (
    id INT AUTO_INCREMENT PRIMARY KEY,
    class_id INT NOT NULL,
    name VARCHAR(32) NOT NULL,
    trigger_text VARCHAR(120) NOT NULL,
    subject_text VARCHAR(32) NULL,
    tools_json JSON NOT NULL,
    description TEXT NOT NULL,
    source_text MEDIUMTEXT NOT NULL,
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    seed_key VARCHAR(64) NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uq_skills_class_name (class_id, name),
    UNIQUE KEY uq_skills_class_seed (class_id, seed_key),
    CONSTRAINT fk_skills_class FOREIGN KEY (class_id) REFERENCES classes(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS tutor_agent_skills (
    agent_id INT NOT NULL,
    skill_id INT NOT NULL,
    PRIMARY KEY (agent_id, skill_id),
    CONSTRAINT fk_agent_skills_agent FOREIGN KEY (agent_id) REFERENCES tutor_agents(id) ON DELETE CASCADE,
    CONSTRAINT fk_agent_skills_skill FOREIGN KEY (skill_id) REFERENCES skills(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS tutor_agent_materials (
    agent_id INT NOT NULL,
    material_id INT NOT NULL,
    PRIMARY KEY (agent_id, material_id),
    CONSTRAINT fk_agent_materials_agent FOREIGN KEY (agent_id) REFERENCES tutor_agents(id) ON DELETE CASCADE,
    CONSTRAINT fk_agent_materials_material FOREIGN KEY (material_id) REFERENCES materials(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS conversations (
    id INT AUTO_INCREMENT PRIMARY KEY,
    class_id INT NOT NULL,
    user_id INT NOT NULL,
    agent_id INT NOT NULL,
    title VARCHAR(64) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_conversations_user_class_updated (user_id, class_id, updated_at),
    CONSTRAINT fk_conversations_class FOREIGN KEY (class_id) REFERENCES classes(id),
    CONSTRAINT fk_conversations_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT fk_conversations_agent FOREIGN KEY (agent_id) REFERENCES tutor_agents(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- role 用 ENUM 而不是 CHECK：在严格模式下插入非法值直接报错，也满足「消息角色
-- 只能是 user / assistant」的数据库约束（design.md Decision 2）。
CREATE TABLE IF NOT EXISTS messages (
    id INT AUTO_INCREMENT PRIMARY KEY,
    conversation_id INT NOT NULL,
    role ENUM('user','assistant') NOT NULL,
    content MEDIUMTEXT NOT NULL,
    citations_json JSON NULL,
    skill_name VARCHAR(32) NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_messages_conversation_id (conversation_id, id),
    CONSTRAINT fk_messages_conversation FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
