// 本文件保存索要意向及简历获取进度，使用独立事务避免重复建档、覆盖正文或累加岗位统计。
package httpapi

import (
	"context"
	"strings"
	"time"
)

// ResumeTrackingInput 是本地程序同步的候选人身份和已核实的简历进度，不含附件、聊天正文或本地路径。
type ResumeTrackingInput struct {
	ID string `json:"id"`
	Name string `json:"candidate_name"`
	State string `json:"state"`
	Score *float64 `json:"score,omitempty"`
	Reason string `json:"reason,omitempty"`
	Error string `json:"error"`
	UpdatedAt time.Time `json:"updated_at"`
}

// resumeStateRank 返回已完成进度的顺序；失败原因独立保存，不使进度倒退。
func resumeStateRank(state string) int {
	switch state {
	case "pending": return 1
	case "requested": return 2
	case "received": return 3
	case "downloaded": return 4
	}
	return 0
}

// applyResumeTracking 合并确定事实；重复、迟到及回退的报告不会覆盖较新的进度和失败原因。
func applyResumeTracking(item *CandidateEngagement, update ResumeTrackingInput) bool {
	if item.ResumeUpdatedAt != nil && !update.UpdatedAt.After(*item.ResumeUpdatedAt) { return false }
	current := item.ResumeState
	if current == "" && item.ResumeRequestedAt != nil { current = "requested" }
	if resumeStateRank(update.State) < resumeStateRank(current) { return false }
	changed := item.ResumeState != update.State || item.ResumeError != update.Error
	item.ResumeState, item.ResumeError = update.State, update.Error
	item.ResumeUpdatedAt = &update.UpdatedAt
	if update.State == "requested" && update.Error == "" && item.ResumeRequestedAt == nil {
		item.ResumeRequestedAt = &update.UpdatedAt
	}
	return changed
}

// resumeTrackingEvent 返回可供详情时间线展示的事件类型。
func resumeTrackingEvent(item ResumeTrackingInput) string {
	if item.Error != "" { return "resume_tracking_failed" }
	return "resume_" + item.State
}

// SaveResumeTracking 原子保存内存候选人档案、岗位关系与进度事件，行为与 PostgreSQL 实现一致。
func (s *MemoryCandidateStore) SaveResumeTracking(position Position, update ResumeTrackingInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var profile PositionCandidate
	for _, value := range s.profiles {
		if value.UserEmail == position.UserEmail && value.PlatformID == position.PlatformID && value.PlatformCandidateID == update.ID {
			profile = value
			break
		}
	}
	if profile.ID == "" {
		profile = PositionCandidate{ID:s.nextID("candidate"), UserEmail:position.UserEmail, PlatformID:position.PlatformID,
			PlatformCandidateID:update.ID, CandidateName:update.Name, CreatedAt:now, FirstSeenAt:&update.UpdatedAt}
	}
	if update.Name != "" { profile.CandidateName = update.Name }
	if update.Score != nil { profile.AIDetailScore, profile.AIDetailReason = update.Score, update.Reason }
	profile.UpdatedAt = now
	s.profiles[profile.ID] = profile
	var engagement CandidateEngagement
	for _, value := range s.engagements {
		if value.CandidateID == profile.ID && value.PositionID == position.ID && value.PlatformAccountID == "" { engagement = value; break }
	}
	if engagement.ID == "" {
		engagement = CandidateEngagement{ID:s.nextID("engagement"), CandidateID:profile.ID, UserEmail:position.UserEmail,
			PositionID:position.ID, PlatformID:position.PlatformID, Status:"pooled", FirstSeenAt:&update.UpdatedAt, CreatedAt:now}
	}
	changed := applyResumeTracking(&engagement, update)
	engagement.UpdatedAt = now
	s.engagements[engagement.ID] = engagement
	if changed {
		reason := update.Reason
		if update.Error != "" { reason = update.Error }
		s.events[profile.ID] = append(s.events[profile.ID], CandidateEvent{ID:s.nextID("event"), CandidateID:profile.ID,
			EngagementID:engagement.ID, PositionID:position.ID, PlatformID:position.PlatformID, EventType:resumeTrackingEvent(update),
			Score:update.Score, Reason:reason, Metadata:map[string]any{"source":"resume_tracking", "state":update.State}, CreatedAt:update.UpdatedAt})
	}
	return nil
}

// SaveResumeTracking 在同一事务中去重建档、锁定岗位关系、推进进度并记录事件；不更新岗位计数。
func (s *PostgresCandidateStore) SaveResumeTracking(position Position, update ResumeTrackingInput) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	userID, err := ensureUserID(ctx, s.db, position.UserEmail)
	if err != nil { return err }
	tenantID, err := userTenantID(ctx, s.db, userID)
	if err != nil { return err }
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return err }
	defer tx.Rollback()
	var candidateID string
	// 仅合并已提供的摘要字段，不使用完整简历覆盖式接口。
	err = tx.QueryRowContext(ctx, `INSERT INTO candidate_profiles
		(tenant_id,created_by_user_id,source_platform_id,source_platform_candidate_id,candidate_name,ai_detail_score,ai_detail_reason,first_seen_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (tenant_id,source_platform_id,source_platform_candidate_id) DO UPDATE SET
		candidate_name=COALESCE(NULLIF(EXCLUDED.candidate_name,''),candidate_profiles.candidate_name),
		ai_detail_score=COALESCE(EXCLUDED.ai_detail_score,candidate_profiles.ai_detail_score),
		ai_detail_reason=CASE WHEN EXCLUDED.ai_detail_score IS NOT NULL THEN EXCLUDED.ai_detail_reason ELSE candidate_profiles.ai_detail_reason END,
		updated_at=now() RETURNING id::text`, tenantID,userID,position.PlatformID,update.ID,update.Name,update.Score,update.Reason,update.UpdatedAt).Scan(&candidateID)
	if err != nil { return err }
	_, err = tx.ExecContext(ctx, `INSERT INTO candidate_engagements
		(tenant_id,candidate_id,position_id,platform_id,status,first_seen_at)
		VALUES ($1,$2,$3,$4,'pooled',$5)
		ON CONFLICT (tenant_id,candidate_id,position_id) WHERE platform_account_id IS NULL DO NOTHING`,
		tenantID,candidateID,position.ID,position.PlatformID,update.UpdatedAt)
	if err != nil { return err }
	var engagement CandidateEngagement
	err = tx.QueryRowContext(ctx, `SELECT id::text,resume_state,resume_error,resume_updated_at,resume_requested_at
		FROM candidate_engagements WHERE tenant_id=$1 AND candidate_id=$2 AND position_id=$3 AND platform_account_id IS NULL FOR UPDATE`,
		tenantID,candidateID,position.ID).Scan(&engagement.ID,&engagement.ResumeState,&engagement.ResumeError,&engagement.ResumeUpdatedAt,&engagement.ResumeRequestedAt)
	if err != nil { return err }
	changed := applyResumeTracking(&engagement, update)
	_, err = tx.ExecContext(ctx, `UPDATE candidate_engagements SET resume_state=$2,resume_error=$3,resume_updated_at=$4,
		resume_requested_at=$5,updated_at=now() WHERE id=$1`,engagement.ID,engagement.ResumeState,engagement.ResumeError,engagement.ResumeUpdatedAt,engagement.ResumeRequestedAt)
	if err != nil { return err }
	if changed {
		reason := update.Reason
		if update.Error != "" { reason = update.Error }
		_, err = tx.ExecContext(ctx, `INSERT INTO candidate_events
			(tenant_id,candidate_id,engagement_id,position_id,platform_id,event_type,score,reason,metadata,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10)`, tenantID,candidateID,engagement.ID,position.ID,position.PlatformID,
			resumeTrackingEvent(update),update.Score,reason,string(toJSONB(map[string]any{"source":"resume_tracking","state":update.State})),update.UpdatedAt)
		if err != nil { return err }
	}
	return tx.Commit()
}

// candidateWithEngagement 将所选岗位的跟踪进度写入候选人展示对象。
func candidateWithEngagement(profile PositionCandidate, item CandidateEngagement) PositionCandidate {
	profile.EngagementID, profile.EngagementStatus, profile.PositionID = item.ID, item.Status, item.PositionID
	profile.DetailFetchedAt, profile.GreetedAt, profile.ResumeRequestedAt = item.DetailFetchedAt, item.GreetedAt, item.ResumeRequestedAt
	profile.ResumeState, profile.ResumeError, profile.ResumeUpdatedAt = item.ResumeState, item.ResumeError, item.ResumeUpdatedAt
	return profile
}

// matchesResumeFilter 判断候选人在当前岗位是否符合简历进度筛选。
func matchesResumeFilter(item CandidateEngagement, filter string) bool {
	switch filter {
	case "detail": return item.DetailFetchedAt != nil
	case "greeted": return item.GreetedAt != nil
	case "resume": return item.ResumeRequestedAt != nil
	case "resume_failed": return item.ResumeError != ""
	}
	if strings.HasPrefix(filter,"resume_") { return item.ResumeState == strings.TrimPrefix(filter,"resume_") }
	return true
}

