// 本文件将 HRPlus 原计划请求加密落盘并按原编号恢复；后台补传只发送已保存的状态和释放事实。
package planoperations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/protectedsession"
	"sync"
)

// Store 保存请求，上传互斥避免同进程多个补传循环并行推进原序号。
type Store struct {
	db       *localdb.DB
	uploadMu sync.Mutex
}

// New 复用当前本地数据库，不创建额外明文凭证文件。
func New(db *localdb.DB) *Store { return &Store{db: db} }

// envelope 将不可变路由、原编号与实际请求一起加密，防止 SQLite 元数据换指向。
type envelope struct {
	Kind      string                         `json:"kind"`
	PlanID    string                         `json:"plan_id"`
	RunID     string                         `json:"run_id"`
	OwnerID   string                         `json:"owner_id"`
	RequestID string                         `json:"request_id"`
	ItemRunID string                         `json:"item_run_id,omitempty"`
	Claim     *cloudapi.PlanClaimRequest     `json:"claim,omitempty"`
	Update    *cloudapi.PlanRunUpdateRequest `json:"update,omitempty"`
	Prepare   *cloudapi.PlanItemTaskRequest  `json:"prepare,omitempty"`
}

// requestHash 为不可变原内容计算摘要，不保存凭证原文。
func requestHash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

// save 加密完成后才访问 SQLite，重复登记返回原密文与原落盘顺序。
func (s *Store) save(ctx context.Context, scope string, e envelope, sequence int64) (localdb.PlanOperation, error) {
	raw, err := json.Marshal(e)
	if err != nil {
		return localdb.PlanOperation{}, err
	}
	defer clear(raw)
	cipher, err := protectedsession.ProtectExecution(scope, raw)
	if err != nil {
		return localdb.PlanOperation{}, err
	}
	return s.db.SavePlanOperation(ctx, localdb.PlanOperation{OwnerScope: scope, RequestID: e.RequestID, PlanID: e.PlanID, RunID: e.RunID, OwnerID: e.OwnerID, Kind: e.Kind, RunSequence: sequence, BodyHash: requestHash(raw), Cipher: cipher})
}

// StageClaim 在任何云端领取前保存原请求；恢复本请求不代表当前仍有本地预留或时间许可。
func (s *Store) StageClaim(ctx context.Context, scope string, input cloudapi.PlanClaimRequest) (localdb.PlanOperation, error) {
	if input.Validate() != nil {
		return localdb.PlanOperation{}, localdb.ErrPlanRequestConflict
	}
	return s.save(ctx, scope, envelope{Kind: "claim", PlanID: input.PlanID, RunID: input.RunID, OwnerID: input.OwnerID, RequestID: input.RequestID, Claim: &input}, 0)
}

// StageUpdate 在发送状态前保存完整原事实，重试不能替换数量、清理结论或序号。
func (s *Store) StageUpdate(ctx context.Context, scope, planID string, input cloudapi.PlanRunUpdateRequest) (localdb.PlanOperation, error) {
	if input.Validate() != nil {
		return localdb.PlanOperation{}, localdb.ErrPlanRequestConflict
	}
	return s.save(ctx, scope, envelope{Kind: input.Action, PlanID: planID, RunID: input.RunID, OwnerID: input.OwnerID, RequestID: input.RequestID, Update: &input}, input.Sequence)
}

// StagePrepare 在云端创建岗位任务前保存原执行项准备请求，后台补传不会发送本请求。
func (s *Store) StagePrepare(ctx context.Context, scope, planID string, input cloudapi.PlanItemTaskRequest) (localdb.PlanOperation, error) {
	if input.Validate() != nil {
		return localdb.PlanOperation{}, localdb.ErrPlanRequestConflict
	}
	return s.save(ctx, scope, envelope{Kind: "prepare_item", PlanID: planID, RunID: input.RunID, ItemRunID: input.ItemRunID, OwnerID: input.OwnerID, RequestID: input.RequestID, Prepare: &input}, 0)
}

// OriginalPrepare 恢复原执行项路由与占用证明，不把恢复请求当作当前页面执行许可。
func (s *Store) OriginalPrepare(ctx context.Context, scope, request string) (cloudapi.PlanItemTaskRequest, error) {
	e, record, err := s.load(ctx, scope, request)
	if err != nil {
		return cloudapi.PlanItemTaskRequest{}, err
	}
	if e.Kind != "prepare_item" || e.Prepare == nil || e.Claim != nil || e.Update != nil || record.RunSequence != 0 {
		return cloudapi.PlanItemTaskRequest{}, localdb.ErrPlanRequestConflict
	}
	input := *e.Prepare
	input.RunID, input.ItemRunID = e.RunID, e.ItemRunID
	if input.OwnerID != e.OwnerID || input.RequestID != e.RequestID || input.Validate() != nil {
		return cloudapi.PlanItemTaskRequest{}, localdb.ErrPlanRequestConflict
	}
	return input, nil
}

// load 核对账号、摘要和全部路由元数据，不允许损坏记录被重新生成成另一份请求。
func (s *Store) load(ctx context.Context, scope, request string) (envelope, localdb.PlanOperation, error) {
	record, err := s.db.PlanOperation(ctx, scope, request)
	if err != nil {
		return envelope{}, record, err
	}
	raw, err := protectedsession.UnprotectExecution(scope, record.Cipher)
	if err != nil {
		return envelope{}, record, err
	}
	defer clear(raw)
	if requestHash(raw) != record.BodyHash {
		return envelope{}, record, localdb.ErrPlanRequestConflict
	}
	var e envelope
	if err = json.Unmarshal(raw, &e); err != nil {
		return e, record, errors.New("原执行请求密文内容不完整")
	}
	if e.Kind != record.Kind || e.PlanID != record.PlanID || e.RunID != record.RunID || e.OwnerID != record.OwnerID || e.RequestID != record.RequestID {
		return envelope{}, record, localdb.ErrPlanRequestConflict
	}
	return e, record, nil
}

// OriginalClaim 只恢复原领取内容；调用者必须重新取得本地执行权并核对名义时间段才可发送。
func (s *Store) OriginalClaim(ctx context.Context, scope, request string) (cloudapi.PlanClaimRequest, error) {
	e, _, err := s.load(ctx, scope, request)
	if err != nil {
		return cloudapi.PlanClaimRequest{}, err
	}
	if e.Kind != "claim" || e.Claim == nil || e.Update != nil || e.Prepare != nil || e.ItemRunID != "" || e.Claim.RunID != e.RunID || e.Claim.OwnerID != e.OwnerID || e.Claim.RequestID != e.RequestID || e.Claim.PlanID != e.PlanID || e.Claim.Validate() != nil {
		return cloudapi.PlanClaimRequest{}, localdb.ErrPlanRequestConflict
	}
	return *e.Claim, nil
}

// OriginalUpdate 恢复原状态与实际收尾结论，补齐已加密保存的路由后核对全部编号。
func (s *Store) OriginalUpdate(ctx context.Context, scope, request string) (cloudapi.PlanRunUpdateRequest, error) {
	e, record, err := s.load(ctx, scope, request)
	if err != nil {
		return cloudapi.PlanRunUpdateRequest{}, err
	}
	if e.Update == nil || e.Claim != nil || e.Prepare != nil || e.ItemRunID != "" || (e.Kind != "status" && e.Kind != "release") {
		return cloudapi.PlanRunUpdateRequest{}, localdb.ErrPlanRequestConflict
	}
	input := *e.Update
	input.Action = e.Kind
	input.RunID = e.RunID
	if input.OwnerID != e.OwnerID || input.RequestID != e.RequestID || input.Sequence != record.RunSequence || input.Validate() != nil {
		return cloudapi.PlanRunUpdateRequest{}, localdb.ErrPlanRequestConflict
	}
	return input, nil
}

// Authority 是本轮上传的进程内登录证明，切换或退出后 StillCurrent 必须变为 false。
type Authority struct {
	Token          string `json:"-"`
	OwnerScope     string
	StillCurrent   func() bool              `json:"-"`
	ConfirmCurrent func(func() error) error `json:"-"`
}

// String 防止上传授权的日志输出登录令牌。
func (a Authority) String() string { return "HRPlus 计划补传授权（令牌隐藏）" }

// GoString 防止调试格式输出登录令牌。
func (a Authority) GoString() string { return a.String() }

// UploadNext 先核对真实账号，再发送一份原状态；领取请求永远不由补传线程发送。
func (s *Store) UploadNext(ctx context.Context, client *cloudapi.Client, a Authority) (bool, error) {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	if a.StillCurrent == nil || !a.StillCurrent() {
		return false, errors.New("计划补传登录证明已变化")
	}
	record, err := s.db.NextPlanUpdate(ctx, a.OwnerScope)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	identity, err := client.SessionIdentity(ctx, a.Token)
	if err != nil {
		return false, err
	}
	if cloudapi.SessionOwnerScope(client.BaseURL, identity.UserEmail) != a.OwnerScope || !a.StillCurrent() {
		return false, errors.New("计划补传所有者或登录证明不匹配")
	}
	input, err := s.OriginalUpdate(ctx, a.OwnerScope, record.RequestID)
	if err != nil {
		return false, err
	}
	if !a.StillCurrent() {
		return false, errors.New("计划补传登录证明已变化")
	}
	if _, err = client.UpdateExecutionPlanRun(ctx, a.Token, input); err != nil {
		return false, err
	}
	if !a.StillCurrent() {
		return false, errors.New("计划原回执已收到，等待同账号重新核对")
	}
	confirm := func() error { return s.db.ConfirmPlanOperation(ctx, a.OwnerScope, record.RequestID, record.BodyHash) }
	if a.ConfirmCurrent != nil {
		err = a.ConfirmCurrent(confirm)
	} else {
		err = confirm()
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
