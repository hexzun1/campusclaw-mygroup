-- 迭代 2：知识库切片表，以及 knowledge_entries 上的切分策略字段。
--
-- 本文件可能被执行两次：全新数据卷上 MySQL 镜像的 docker-entrypoint-initdb.d 会先跑一遍，
-- 随后 api 启动时的迁移执行器（internal/db.Migrate）因为 schema_migrations 里没有记录而再跑一遍。
-- 因此这里全部写成可重复执行的：建表用 IF NOT EXISTS，加列用 information_schema 条件 +
-- PREPARE/EXECUTE（MySQL 8.0 的 ALTER TABLE 不支持 IF NOT EXISTS，而 DELIMITER 是客户端指令、
-- 存储过程体里的分号也不适合多语句协议，故采用这种只含单语句的写法）。

CREATE TABLE IF NOT EXISTS knowledge_chunks (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    knowledge_entry_id INT NOT NULL,
    material_id INT NOT NULL,
    class_id INT NOT NULL,
    chunk_index INT NOT NULL,
    char_start INT NOT NULL,
    char_end INT NOT NULL,
    chunk_text TEXT NOT NULL,
    index_status ENUM('pending', 'indexed', 'failed') NOT NULL DEFAULT 'pending',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uq_chunks_entry_index (knowledge_entry_id, chunk_index),
    KEY idx_chunks_class_status (class_id, index_status),
    KEY idx_chunks_material (material_id),
    FULLTEXT KEY ft_chunks_text (chunk_text) WITH PARSER ngram,
    CONSTRAINT fk_chunks_entry FOREIGN KEY (knowledge_entry_id) REFERENCES knowledge_entries (id) ON DELETE CASCADE,
    CONSTRAINT fk_chunks_material FOREIGN KEY (material_id) REFERENCES materials (id) ON DELETE CASCADE,
    CONSTRAINT fk_chunks_class FOREIGN KEY (class_id) REFERENCES classes (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 最近一次使用的切分策略与参数（详情展示与重建默认值，design.md Decision 1）。
SET @add_chunk_strategy := IF(
    (SELECT COUNT(*) FROM information_schema.COLUMNS
     WHERE TABLE_SCHEMA = DATABASE()
       AND TABLE_NAME = 'knowledge_entries'
       AND COLUMN_NAME = 'chunk_strategy') = 0,
    'ALTER TABLE knowledge_entries ADD COLUMN chunk_strategy VARCHAR(16) NOT NULL DEFAULT ''auto''',
    'DO 0');
PREPARE stmt FROM @add_chunk_strategy;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @add_chunk_params := IF(
    (SELECT COUNT(*) FROM information_schema.COLUMNS
     WHERE TABLE_SCHEMA = DATABASE()
       AND TABLE_NAME = 'knowledge_entries'
       AND COLUMN_NAME = 'chunk_params') = 0,
    'ALTER TABLE knowledge_entries ADD COLUMN chunk_params JSON NULL DEFAULT NULL',
    'DO 0');
PREPARE stmt FROM @add_chunk_params;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
