package sqldb

import "testing"

// hasWriteIntent:写/锁定读/数据修改 CTE 判为主库;纯读判为副本。偏保守。
func TestHasWriteIntent(t *testing.T) {
	primary := []string{
		"INSERT INTO t VALUES(1)",
		"  update t set a=1",
		"DELETE FROM t",
		"REPLACE INTO t VALUES(1)",
		"insert into t(v) values('x') returning id", // RETURNING(走 QueryRow 却是写)
		"SELECT * FROM t WHERE id=1 FOR UPDATE",     // 锁定读
		"select * from t for share",
		"WITH x AS (DELETE FROM t RETURNING id) SELECT * FROM x", // 数据修改 CTE
		"( INSERT INTO t VALUES(1) )",                            // 前导括号/空白
	}
	for _, q := range primary {
		if !hasWriteIntent(q) {
			t.Errorf("应判为主库(写意图): %q", q)
		}
	}
	replica := []string{
		"SELECT * FROM t",
		"  select count(*) from t where a=?",
		"WITH x AS (SELECT * FROM t) SELECT * FROM x", // 只读 CTE
	}
	for _, q := range replica {
		if hasWriteIntent(q) {
			t.Errorf("应判为副本(纯读): %q", q)
		}
	}
}
