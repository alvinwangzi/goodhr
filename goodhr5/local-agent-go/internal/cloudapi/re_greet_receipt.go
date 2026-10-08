// 本文件提交 HRPlus 已确认复打的原始收据，并区分登录失效、事实冲突和可重试网络错误。
package cloudapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// ReGreetReceiptRequest 保存不可变发送事实，不包含登录 Token。
type ReGreetReceiptRequest struct {
	MachineID string `json:"machine_id"`
	OperationID         string    `json:"operation_id"`
	Platform            string    `json:"platform"`
	PlatformCandidateID string    `json:"platform_candidate_id"`
	CandidateName       string    `json:"candidate_name"`
	Success             bool      `json:"success"`
	RunID               string    `json:"run_id"`
	BaseCount           int       `json:"base_count"`
	BaseContactAt       time.Time `json:"base_contact_at"`
	SentAt              time.Time `json:"sent_at"`
	MessageText         string    `json:"message_text"`
}

// ReGreetReceiptResponse 保存服务器已经提交的原记账结果。
type ReGreetReceiptResponse struct {
	OperationID string    `json:"operation_id"`
	ResultCount int       `json:"result_count"`
	SentAt      time.Time `json:"sent_at"`
	ReceivedAt  time.Time `json:"received_at"`
}

// ReceiptUploadError 提供可判定的补传类别，避免把事实冲突当网络重试。
type ReceiptUploadError struct {
	Status  int
	Message string
}

// Error 返回适合调用方展示的补传错误。
func (e ReceiptUploadError) Error() string { return e.Message }

// UploadReGreetReceipt 用原编号上报成功事实，收到旧服务的泛成功响应也不能标为已确认。
func (c *Client) UploadReGreetReceipt(ctx context.Context, token, positionID string, request ReGreetReceiptRequest) (ReGreetReceiptResponse, error) {
	result := ReGreetReceiptResponse{}
	payload, code, err := c.postAuthed(ctx, token, "/api/positions/"+url.PathEscape(positionID)+"/re-greet-report", request)
	if err != nil {
		return result, err
	}
	if code >= 400 {
		return result, ReceiptUploadError{Status: code, Message: cloudMessage(payload, "复打收据上报失败")}
	}
	raw, err := json.Marshal(payload["receipt"])
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	if result.OperationID != request.OperationID || result.ResultCount != request.BaseCount+1 || result.SentAt.IsZero() || result.ReceivedAt.IsZero() {
		return result, fmt.Errorf("云端未返回匹配的复打收据，保留待核对")
	}
	return result, nil
}
