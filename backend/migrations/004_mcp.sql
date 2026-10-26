-- MCP 工具服务器、工具发现缓存与调用审计。
--
-- 与 002/003 相同的原因，本文件可能被执行两次：全新数据卷上 MySQL 镜像的
-- docker-entrypoint-initdb.d 会先跑一遍，随后 api 启动时的迁移执行器
-- （internal/db.Migrate）因为 schema_migrations 里没有记录而再跑一遍。
-- 因此这里全部写成可重复执行的：CREATE TABLE IF NOT EXISTS。

CREATE TABLE IF NOT EXISTS mcp_servers (
    id INT AUTO_INCREMENT PRIMARY KEY,
    class_id INT NOT NULL,
    name VARCHAR(32) NOT NULL,
    base_url VARCHAR(200) NOT NULL,
    -- 宿主必须把令牌原样放进 Authorization 头，无法只存哈希；读取服务器的
    -- 默认查询不选这一列，只有 GetServerToken 取。见 design.md Decision 5。
    auth_token VARBINARY(512) NOT NULL,
    status ENUM('disconnected','connected','error') NOT NULL DEFAULT 'disconnected',
    last_error VARCHAR(255) NULL,
    tools_refreshed_at DATETIME NULL,
    created_by INT NOT NULL,
    seed_key VARCHAR(64) NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uq_mcp_servers_class_name (class_id, name),
    UNIQUE KEY uq_mcp_servers_class_seed (class_id, seed_key),
    CONSTRAINT fk_mcp_servers_class FOREIGN KEY (class_id) REFERENCES classes(id),
    CONSTRAINT fk_mcp_servers_creator FOREIGN KEY (created_by) REFERENCES users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS mcp_tools (
    server_id INT NOT NULL,
    name VARCHAR(64) NOT NULL,
    description VARCHAR(1024) NOT NULL,
    input_schema JSON NOT NULL,
    PRIMARY KEY (server_id, name),
    CONSTRAINT fk_mcp_tools_server FOREIGN KEY (server_id) REFERENCES mcp_servers(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS mcp_tool_calls (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    class_id INT NOT NULL,
    user_id INT NOT NULL,
    conversation_id INT NOT NULL,
    -- 服务器删除后审计仍可读：server_id 置空，冗余的 server/tool 名称保留。
    server_id INT NULL,
    server VARCHAR(32) NOT NULL,
    tool VARCHAR(64) NOT NULL,
    status ENUM('ok','error','denied') NOT NULL,
    reason VARCHAR(32) NULL,
    `round` TINYINT NOT NULL,
    duration_ms INT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_mcp_tool_calls_class_created (class_id, created_at),
    CONSTRAINT fk_mcp_tool_calls_class FOREIGN KEY (class_id) REFERENCES classes(id),
    CONSTRAINT fk_mcp_tool_calls_user FOREIGN KEY (user_id) REFERENCES users(id),
    CONSTRAINT fk_mcp_tool_calls_conversation FOREIGN KEY (conversation_id) REFERENCES conversations(id),
    CONSTRAINT fk_mcp_tool_calls_server FOREIGN KEY (server_id) REFERENCES mcp_servers(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
