// 本文件定义 HRPlus 手动与计划共用的账号执行权，凭证由本地生成并只以摘要保存。
package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrAccountExecutionBusy     = errors.New("账号有任务正在执行或收尾，请等待")
	ErrAccountExecutionConflict = errors.New("执行权请求编号对应的内容已变化")
	ErrAccountExecutionReleased = errors.New("原执行权已释放，不能再次启动")
	ErrAccountExecutionProof    = errors.New("执行权凭证或收尾确认不匹配")
)

// AccountExecutionClaim 携带本地已持久保存的编号与随机凭证，不接受登录令牌作为执行权。
type AccountExecutionClaim struct {
	UserEmail     string
	MachineID     string
	OwnerType     string
	OwnerID       string
	RequestID     string
	Credential    string
	LocalReserved bool
}

// AccountExecutionOwner 是安全的占用快照，不包含凭证及登录令牌。
type AccountExecutionOwner struct {
	OwnerID   string `json:"owner_id"`
	OwnerType string `json:"owner_type"`
	MachineID string `json:"machine_id"`
	State     string `json:"state"`
}

// PostgresAccountExecutionStore 在账号事务锁内领取、核对及严格释放执行权。
type PostgresAccountExecutionStore struct{ db *sql.DB }

// NewPostgresAccountExecutionStore 创建共用执行权存储，业务权限仍由调用服务核对。
func NewPostgresAccountExecutionStore(db *sql.DB) *PostgresAccountExecutionStore {
	return &PostgresAccountExecutionStore{db: db}
}

// accountCredentialHash 保存凭证摘要，调用方必须生成至少三十二字节的随机凭证。
func accountCredentialHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// validateAccountClaim 拒绝缺失本地预留或不完整的执行权请求。
func validateAccountClaim(c AccountExecutionClaim) error {
	if c.UserEmail == "" || c.UserEmail != strings.ToLower(strings.TrimSpace(c.UserEmail)) || c.MachineID == "" || c.OwnerID == "" || c.RequestID == "" || len(c.Credential) < 32 || !c.LocalReserved || (c.OwnerType != "manual" && c.OwnerType != "plan") {
		return fmt.Errorf("执行权请求缺少账号、设备、编号、凭证或本地预留确认")
	}
	return nil
}

// accountRequestHash 将不可变请求字段连同凭证摘要绑定，避免同编号换设备或换动作。
func accountRequestHash(c AccountExecutionClaim, kind string, cleaned bool) string {
	raw, _ := json.Marshal([]any{c.UserEmail, c.MachineID, c.OwnerType, c.OwnerID, c.RequestID, accountCredentialHash(c.Credential), c.LocalReserved, kind, cleaned})
	return accountCredentialHash(string(raw))
}

// lockAccountExecution 与旧式岗位启动使用同一账号锁，不通过心跳超时清理占用。
func lockAccountExecution(ctx context.Context, tx *sql.Tx, email string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, email)
	return err
}

// accountRequestReplay 核对成功请求的原内容，失败或等待请求可按原编号重试。
func accountRequestReplay(ctx context.Context, tx *sql.Tx, c AccountExecutionClaim, kind string, cleaned bool) (bool, error) {
	var stored string
	err := tx.QueryRowContext(ctx, `SELECT body_hash FROM account_execution_requests WHERE account_key=$1 AND request_id=$2`, c.UserEmail, c.RequestID).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if stored != accountRequestHash(c, kind, cleaned) {
		return false, ErrAccountExecutionConflict
	}
	return true, nil
}

// recordAccountRequest 与占用更新同事务保存成功回执，释放后仍可识别旧领取请求。
func recordAccountRequest(ctx context.Context, tx *sql.Tx, c AccountExecutionClaim, kind string, cleaned bool) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO account_execution_requests(account_key,request_id,kind,body_hash,owner_id) VALUES($1,$2,$3,$4,$5)`, c.UserEmail, c.RequestID, kind, accountRequestHash(c, kind, cleaned), c.OwnerID)
	return err
}

// accountOwnerProof 只返回与设备、占用者及凭证摘要完全匹配的当前占用。
func accountOwnerProof(ctx context.Context, tx *sql.Tx, c AccountExecutionClaim) (AccountExecutionOwner, error) {
	var o AccountExecutionOwner
	err := tx.QueryRowContext(ctx, `SELECT owner_id,owner_type,machine_id,state FROM account_execution_owners WHERE account_key=$1 AND owner_id=$2 AND machine_id=$3 AND owner_type=$4 AND credential_hash=$5`, c.UserEmail, c.OwnerID, c.MachineID, c.OwnerType, accountCredentialHash(c.Credential)).Scan(&o.OwnerID, &o.OwnerType, &o.MachineID, &o.State)
	if errors.Is(err, sql.ErrNoRows) {
		return o, ErrAccountExecutionProof
	}
	return o, err
}

// Claim 在统一账号锁内保留 starting 占用，成功请求重试不会新建占用者。
func (s *PostgresAccountExecutionStore) Claim(ctx context.Context, c AccountExecutionClaim) (AccountExecutionOwner, error) {
	if err := validateAccountClaim(c); err != nil {
		return AccountExecutionOwner{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountExecutionOwner{}, err
	}
	defer tx.Rollback()
	if err = lockAccountExecution(ctx, tx, c.UserEmail); err != nil {
		return AccountExecutionOwner{}, err
	}
	owner, err := claimAccountExecutionTx(ctx, tx, c)
	if err != nil {
		return AccountExecutionOwner{}, err
	}
	if err = tx.Commit(); err != nil {
		return AccountExecutionOwner{}, err
	}
	return owner, nil
}

// claimAccountExecutionTx 复用调用方已持有的账号事务锁，使计划运行和账号领取同事务提交。
func claimAccountExecutionTx(ctx context.Context, tx *sql.Tx, c AccountExecutionClaim) (AccountExecutionOwner, error) {
	replay, err := accountRequestReplay(ctx, tx, c, "claim", false)
	if err != nil {
		return AccountExecutionOwner{}, err
	}
	if replay {
		o, e := accountOwnerProof(ctx, tx, c)
		if errors.Is(e, ErrAccountExecutionProof) {
			e = ErrAccountExecutionReleased
		}
		return o, e
	}
	var ownerUsed bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_execution_requests WHERE owner_id=$1 AND kind='claim')`, c.OwnerID).Scan(&ownerUsed); err != nil {
		return AccountExecutionOwner{}, err
	}
	if ownerUsed {
		return AccountExecutionOwner{}, ErrAccountExecutionReleased
	}
	var busy bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_execution_owners WHERE account_key=$1) OR EXISTS(SELECT 1 FROM positions p JOIN users u ON u.id=p.user_id WHERE u.email=$1 AND p.status='running')`, c.UserEmail).Scan(&busy)
	if err != nil {
		return AccountExecutionOwner{}, err
	}
	if busy {
		return AccountExecutionOwner{}, ErrAccountExecutionBusy
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO account_execution_owners(account_key,user_email,machine_id,owner_type,owner_id,credential_hash,state) VALUES($1,$1,$2,$3,$4,$5,'starting')`, c.UserEmail, c.MachineID, c.OwnerType, c.OwnerID, accountCredentialHash(c.Credential))
	if err != nil {
		return AccountExecutionOwner{}, err
	}
	if err = recordAccountRequest(ctx, tx, c, "claim", false); err != nil {
		return AccountExecutionOwner{}, err
	}
	return AccountExecutionOwner{OwnerID: c.OwnerID, OwnerType: c.OwnerType, MachineID: c.MachineID, State: "starting"}, nil
}

// ConfirmRunning 只有本地完成准备并确认实际开始后才更新 running，不允许收尾重新进入运行。
func (s *PostgresAccountExecutionStore) ConfirmRunning(ctx context.Context, c AccountExecutionClaim) error {
	if err := validateAccountClaim(c); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockAccountExecution(ctx, tx, c.UserEmail); err != nil {
		return err
	}
	o, err := accountOwnerProof(ctx, tx, c)
	if err != nil {
		return err
	}
	if o.State == "releasing" {
		return ErrAccountExecutionProof
	}
	if _, err = tx.ExecContext(ctx, `UPDATE account_execution_owners SET state='running' WHERE account_key=$1`, c.UserEmail); err != nil {
		return err
	}
	return tx.Commit()
}

// Release 只有同一占用者提交收尾确认才删除占用，旧释放重试不能删除后来运行。
func (s *PostgresAccountExecutionStore) Release(ctx context.Context, c AccountExecutionClaim, cleaned bool) error {
	if err := validateAccountClaim(c); err != nil {
		return err
	}
	if !cleaned {
		return ErrAccountExecutionProof
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockAccountExecution(ctx, tx, c.UserEmail); err != nil {
		return err
	}
	replay, err := accountRequestReplay(ctx, tx, c, "release", cleaned)
	if err != nil {
		return err
	}
	if replay {
		return nil
	}
	if _, err = accountOwnerProof(ctx, tx, c); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM account_execution_owners WHERE account_key=$1 AND owner_id=$2`, c.UserEmail, c.OwnerID); err != nil {
		return err
	}
	if err = recordAccountRequest(ctx, tx, c, "release", cleaned); err != nil {
		return err
	}
	return tx.Commit()
}
