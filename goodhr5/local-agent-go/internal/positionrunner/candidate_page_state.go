// 本文件负责同步招聘平台页面核实的既有事实；平台读取页面，公共流程负责存储与跳过决策。
package positionrunner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"goodhr5/local-agent-go/internal/cloudapi"
	"goodhr5/local-agent-go/internal/localdb"
	"goodhr5/local-agent-go/internal/platformcore"
)

// reconcileCandidatePageState 同步已经沟通或已收简历的正向事实，不推断未知时间或回退已有状态。
func (r *Runner) reconcileCandidatePageState(ctx context.Context, position localdb.Position, options StartOptions, candidateID, name string, state platformcore.CandidatePageState) error {
	if !state.ContactObserved && state.ResumeStatus != "received" {
		return nil
	}
	if strings.TrimSpace(candidateID) == "" {
		return fmt.Errorf("页面状态缺少稳定候选人 ID，不能按姓名同步")
	}
	contacted := state.ContactObserved || state.ResumeStatus == "received"
	if err := r.db.ObserveCandidateState(ctx, position.ID, candidateID, contacted, state.ResumeStatus); err != nil {
		return err
	}
	base := strings.TrimSpace(options.CloudAPIBase)
	if base == "" {
		base = strings.TrimSpace(r.cloudAPIBase)
	}
	if base == "" || options.Token == "" {
		return fmt.Errorf("页面事实已在本地核对，缺少登录信息，云端状态暂未同步")
	}
	syncCtx, cancel := context.WithTimeout(ctx, cloudStatsSyncTimeout)
	defer cancel()
	// 复用扫描记录入口，观察来源不会被服务端当作真实发送。
	resumeStatus := state.ResumeStatus
	if resumeStatus != "received" && resumeStatus != "requested" {
		resumeStatus = ""
	}
	err := cloudapi.New(base).ReportScreenings(syncCtx, options.Token, position.ID, []cloudapi.ScreeningRecord{{Platform: position.PlatformID, PlatformCandidateID: candidateID, CandidateName: name, Source: "platform_observation", ContactObserved: contacted, ResumeStatus: resumeStatus}})
	if err == nil {
		r.positionContextLog(ctx, position.ID, "info", "平台页面状态已同步：候选人="+name+"，实际招呼时间保持原值")
	}
	return err
}

// candidateStateFromReply 将已经核对身份的会话内容转换为页面事实，系统提示不代表沟通。
func candidateStateFromReply(current platformcore.ReplyContext) platformcore.CandidatePageState {
	state := platformcore.CandidatePageState{ResumeStatus: current.ResumeStatus}
	for _, message := range current.Messages {
		if message.Direction == "outbound" || message.Direction == "inbound" {
			state.ContactObserved = true
		}
	}
	return state
}

// skipFirstGreetFromPage 核对页面与已有系统记录，已有沟通或已收简历时同步并阻止重复首次招呼。
func (r *Runner) skipFirstGreetFromPage(ctx context.Context, position localdb.Position, runtime platformcore.Runtime, exec platformExecutor, cfg cloudapi.PlatformConfig, candidate map[string]any, options StartOptions) (bool, error) {
	state := platformcore.CandidatePageState{}
	if reader, ok := runtime.(platformcore.CandidateStateReader); ok {
		value, err := reader.ReadCandidateState(ctx, exec, cfg, platformcore.Candidate(candidate))
		if err != nil {
			return false, err
		}
		state = value
	}
	// 云端同步曾失败时仍保留本地核实的事实，不能因重新扫描而再次发送首次招呼。
	if known, err := r.db.GetCandidate(stringFromMap(candidate, "id"), position.ID); err == nil {
		ext := mapValue(known["ext"])
		state.ContactObserved = state.ContactObserved || boolFromMap(ext, "contact_observed")
		if stringFromMap(ext, "resume_status") == "received" {
			state.ResumeStatus = "received"
		}
	}
	base := strings.TrimSpace(options.CloudAPIBase)
	if base == "" {
		base = strings.TrimSpace(r.cloudAPIBase)
	}
	if base != "" && options.Token != "" && stringFromMap(candidate, "id") != "" {
		known, err := cloudapi.New(base).FindScreening(ctx, options.Token, position.ID, position.PlatformID, stringFromMap(candidate, "id"))
		if err == nil && known != nil {
			state.ContactObserved = state.ContactObserved || known.ContactObserved
			if known.ResumeStatus == "received" {
				state.ResumeStatus = "received"
			}
		}
	}
	if !state.ContactObserved && state.ResumeStatus != "received" {
		return false, nil
	}
	candidate["contact_observed"] = true
	candidate["resume_status"] = state.ResumeStatus
	// 主循环随后保存整份候选人，须携带这些摘要，避免覆盖刚核对的本地状态。
	ext := mapValue(candidate["ext"])
	ext["contact_observed"] = true
	ext["platform_observed_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	if state.ResumeStatus == "received" {
		ext["resume_status"] = "received"
	}
	candidate["ext"] = ext
	candidate["status"] = "contacted"
	if state.ResumeStatus == "received" {
		candidate["status"] = "resume_received"
	}
	return true, r.reconcileCandidatePageState(ctx, position, options, stringFromMap(candidate, "id"), candidateLogName(candidate), state)
}
