// Package regreetupload 独立补传 HRPlus 已确认复打结果，不获取浏览器租约，不重新生成或发送消息。
package regreetupload

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
)

// Client 提供已有云端收据接口，便于注入受控网络故障。
type Client interface {
	UploadReGreetReceipt(context.Context, string, string, cloudapi.ReGreetReceiptRequest) (cloudapi.ReGreetReceiptResponse, error)
}

// LegacyOwnerVerifier 提供原执行任务的只读所有者证明，不凭旧默认作用域猜测归属。
type LegacyOwnerVerifier interface {
	VerifyTaskRunOwner(context.Context, string, string, string, string, string, string) (bool, error)
}

// Uploader 将队列处理串行化，登录信息只通过当前进程调用传入。
type Uploader struct {
	DB        *localdb.DB
	NewClient func(string) Client
	Now       func() time.Time
	mu        sync.Mutex
}

// New 创建补传器，复用现有 HTTP 客户端和 SQLite，不新增依赖或保存 Token。
func New(db *localdb.DB) *Uploader {
	return &Uploader{DB: db, NewClient: func(base string) Client { return cloudapi.New(base) }, Now: time.Now}
}

// Flush 补传当前已验证登录作用域的到期记录，返回本轮云端确认数量。
func (u *Uploader) Flush(ctx context.Context, scope, token string) (int, error) {
	return u.flush(ctx, scope, token, "", "")
}

// FlushLegacyOwned 只补传与当前云端地址及原任务所有者完全匹配的旧收据，不改变原编号或发送事实。
func (u *Uploader) FlushLegacyOwned(ctx context.Context, scope, token, ownerScope, apiBase string) (int, error) {
	if ownerScope == "" || apiBase == "" {
		return 0, nil
	}
	return u.flush(ctx, scope, token, ownerScope, apiBase)
}

// flush 串行处理到期队列；旧数据额外核对服务端所有者后才提交原收据。
func (u *Uploader) flush(ctx context.Context, scope, token, legacyOwnerScope, legacyAPIBase string) (int, error) {
	if scope == "" || token == "" {
		return 0, nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	entries, err := u.DB.DueReGreetOutbox(ctx, scope, u.Now(), 20)
	if err != nil {
		return 0, err
	}
	acknowledged := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return acknowledged, err
		}
		var body struct {
			PositionID string `json:"position_id"`
			cloudapi.ReGreetReceiptRequest
		}
		if err := json.Unmarshal(entry.Payload, &body); err != nil || body.PositionID == "" || body.OperationID != entry.OperationID {
			if err := u.DB.FinishReGreetUpload(ctx, entry, "conflict", "invalid_payload", u.Now()); err != nil {
				return acknowledged, err
			}
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		client := u.NewClient(entry.APIBase)
		if legacyOwnerScope != "" {
			verified := false
			var verifyErr error
			if strings.TrimRight(strings.TrimSpace(entry.APIBase), "/") == strings.TrimRight(strings.TrimSpace(legacyAPIBase), "/") {
				if verifier, ok := client.(LegacyOwnerVerifier); ok {
					verified, verifyErr = verifier.VerifyTaskRunOwner(callCtx, token, body.RunID, body.PositionID, body.Platform, body.MachineID, legacyOwnerScope)
				}
			}
			if !verified || verifyErr != nil {
				cancel()
				status, code := "auth_required", "original_owner_required"
				if verifyErr != nil {
					var auth cloudapi.AuthExpiredError
					if !errors.As(verifyErr, &auth) {
						status, code = "pending", "owner_check_network"
					}
				}
				if err := u.DB.FinishReGreetUpload(context.WithoutCancel(ctx), entry, status, code, u.Now()); err != nil && !errors.Is(err, localdb.ErrAutoReplyConflict) {
					return acknowledged, err
				}
				continue
			}
		}
		receipt, uploadErr := client.UploadReGreetReceipt(callCtx, token, body.PositionID, body.ReGreetReceiptRequest)
		cancel()
		status, code := "pending", "network"
		if uploadErr == nil && receipt.OperationID == entry.OperationID {
			status, code = "acked", ""
		} else {
			var remote cloudapi.ReceiptUploadError
			var auth cloudapi.AuthExpiredError
			if errors.As(uploadErr, &auth) {
				status, code = "auth_required", "auth"
			}
			if errors.As(uploadErr, &remote) {
				switch remote.Status {
				case 401, 403:
					status, code = "auth_required", "auth"
				case 400, 404, 409, 422:
					status, code = "conflict", "receipt_conflict"
				}
			}
		}
		if err := u.DB.FinishReGreetUpload(context.WithoutCancel(ctx), entry, status, code, u.Now()); err != nil && !errors.Is(err, localdb.ErrAutoReplyConflict) {
			return acknowledged, err
		}
		if status == "acked" {
			acknowledged++
		}
		if status == "auth_required" {
			break
		}
	}
	return acknowledged, nil
}
