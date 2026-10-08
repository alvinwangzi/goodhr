// 本文件原子绑定 HRPlus 单岗位运行的真实账号作用域，并保存云端所有者到补传作用域的已核对关系。
package localdb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// BindActionRunAccount 在任何候选人动作前绑定账号；同一运行不允许换账号，绑定与所有者登记同事务保存。
func (db *DB) BindActionRunAccount(ctx context.Context, runID, scope, ownerScope string) (ActionCheckpoint, error) {
	if runID == "" || scope == "" || ownerScope == "" {
		return ActionCheckpoint{}, fmt.Errorf("账号绑定证明不完整")
	}
	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return ActionCheckpoint{}, err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT checkpoint FROM action_runs WHERE run_id=?`, runID).Scan(&raw); err != nil {
		return ActionCheckpoint{}, err
	}
	var checkpoint ActionCheckpoint
	if err = json.Unmarshal([]byte(raw), &checkpoint); err != nil {
		return checkpoint, err
	}
	var registered int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM action_account_scopes WHERE profile_scope=?`, checkpoint.ProfileScope).Scan(&registered); err != nil {
		return checkpoint, err
	}
	alreadyBound := checkpoint.AccountBound || registered > 0
	if alreadyBound && checkpoint.ProfileScope != scope {
		return checkpoint, fmt.Errorf("本次运行的平台账号已变化，请停止后重新开始")
	}
	if !alreadyBound {
		var candidates int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM action_candidates WHERE run_id=?`, runID).Scan(&candidates); err != nil {
			return checkpoint, err
		}
		if candidates != 0 || checkpoint.Scanned != 0 || checkpoint.Greeted != 0 || checkpoint.Replied != 0 || checkpoint.ReGreeted != 0 {
			return checkpoint, fmt.Errorf("任务已开始处理，不能再改变账号作用域")
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `INSERT INTO action_account_scopes(profile_scope,cloud_owner_scope,platform,verified_at) VALUES(?,?,?,?) ON CONFLICT(profile_scope) DO NOTHING`, scope, ownerScope, checkpoint.Platform, now); err != nil {
		return checkpoint, err
	}
	var storedOwner, storedPlatform string
	if err = tx.QueryRowContext(ctx, `SELECT cloud_owner_scope,platform FROM action_account_scopes WHERE profile_scope=?`, scope).Scan(&storedOwner, &storedPlatform); err != nil {
		return checkpoint, err
	}
	if storedOwner != ownerScope || storedPlatform != checkpoint.Platform {
		return checkpoint, ErrIdentityConflict
	}
	checkpoint.ProfileScope, checkpoint.AccountBound = scope, true
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return checkpoint, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE action_runs SET profile_scope=?,checkpoint=?,updated_at=? WHERE run_id=?`, scope, string(encoded), now, runID); err != nil {
		return checkpoint, err
	}
	if err = tx.Commit(); err != nil {
		return checkpoint, err
	}
	return checkpoint, nil
}

// AccountScopesForOwner 读取已核对云端所有者的账号作用域，补传不依赖招聘页面或当前岗位仍运行。
func (db *DB) AccountScopesForOwner(ctx context.Context, ownerScope string) ([]string, error) {
	if ownerScope == "" {
		return nil, fmt.Errorf("缺少已核对的云端账号")
	}
	rows, err := db.conn.QueryContext(ctx, `SELECT profile_scope FROM action_account_scopes WHERE cloud_owner_scope=? ORDER BY verified_at,profile_scope`, ownerScope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var scopes []string
	for rows.Next() {
		var scope string
		if err = rows.Scan(&scope); err != nil {
			return nil, err
		}
		scopes = append(scopes, scope)
	}
	return scopes, rows.Err()
}
