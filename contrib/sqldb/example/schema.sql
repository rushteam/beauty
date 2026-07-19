-- 示例 schema(sqlc 用它推断类型;真实迁移工具另行管理)。
CREATE TABLE authors (
    id   INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    bio  TEXT
);
