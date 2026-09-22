// 本文件负责执行任务运行记录的 PostgreSQL 存储实现。
// 名单统计基于候选人事件表实时重算，保证与实际打招呼和索要简历行为一致。
package httpapi

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"
)

// PostgresTaskRunStore 使用 PostgreSQL 持久化执行任务运行记录。
type PostgresTaskRunStore struct {
	db *sql.DB
}

// NewPostgresTaskRunStore 创建 PostgreSQL 执行任务存储。
func NewPostgresTaskRunStore(db *sql.DB) *PostgresTaskRunStore {
	return &PostgresTaskRunStore{db: db}
}

// CreateTaskRun 创建一条运行记录并返回带 ID 的结果。
// run 为运行记录内容，返回保存后的记录。
func (s *PostgresTaskRunStore) CreateTaskRun(run TaskRun) (TaskRun, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	userID, err := ensureUserID(ctx, s.db, run.UserEmail)
	if err != nil {
		return TaskRun{}, err
	}
	var saved TaskRun
	err = s.db.QueryRowContext(
		ctx,
		`
		INSERT INTO task_runs (
			tenant_id, user_id, position_id, platform_id,
			task_type, machine_id, status, started_at
		)
		VALUES ($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,now())
		RETURNING id, COALESCE(position_id::text,''), platform_id, task_type, machine_id, status, created_at, started_at
		`,
		run.TenantID,
		userID,
		run.PositionID,
		run.PlatformID,
		firstNonEmpty(strings.TrimSpace(run.TaskType), "greeting"),
		strings.TrimSpace(run.MachineID),
		firstNonEmpty(strings.TrimSpace(run.Status), "running"),
	).Scan(
		&saved.ID,
		&saved.PositionID,
		&saved.PlatformID,
		&saved.TaskType,
		&saved.MachineID,
		&saved.Status,
		&saved.CreatedAt,
		&saved.StartedAt,
	)
	if err != nil {
		return TaskRun{}, err
	}
	saved.TenantID = run.TenantID
	saved.UserEmail = run.UserEmail
	saved.PositionName = run.PositionName
	return saved, nil
}

// FinishTaskRun 结束运行记录，本地同步的跳过和失败计数直接写入，
// 打招呼、索要简历和保存人数按候选人事件表重算。
// runID 为运行 ID，status 为结束状态，skipped 和 failed 为本地同步计数。
func (s *PostgresTaskRunStore) FinishTaskRun(runID string, status string, errorMessage string, skipped int, failed int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := s.db.ExecContext(
		ctx,
		`
		UPDATE task_runs SET
			status = $2,
			error_message = $3,
			skipped_count = $4,
			failed_count = $5,
			finished_at = now(),
			greeted_count = (SELECT COUNT(*) FROM candidate_events WHERE task_id = $1 AND event_type = 'greeted_sent'),
			resume_requested_count = (SELECT COUNT(*) FROM candidate_events WHERE task_id = $1 AND event_type = 'resume_requested'),
			scanned_count = (SELECT COUNT(DISTINCT candidate_id) FROM candidate_events WHERE task_id = $1)
		WHERE id = $1
		`,
		runID,
		status,
		strings.TrimSpace(errorMessage),
		maxIntValue(0, skipped),
		maxIntValue(0, failed),
	)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ActiveTaskRunByPosition 返回岗位当前运行中的最新记录。
// positionID 为岗位 ID，没有运行中记录时返回 ErrNotFound。
func (s *PostgresTaskRunStore) ActiveTaskRunByPosition(positionID string) (TaskRun, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(
		ctx,
		taskRunSelectSQL+` WHERE tr.position_id = $1::uuid AND tr.status = 'running' ORDER BY tr.created_at DESC LIMIT 1`,
		positionID,
	)
	if err != nil {
		return TaskRun{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return TaskRun{}, ErrNotFound
	}
	run, err := scanTaskRun(rows)
	if err != nil {
		return TaskRun{}, err
	}
	return run, nil
}

// TaskRunByID 读取团队内一条运行记录详情。
// tenantID 为团队 ID，runID 为运行 ID。
func (s *PostgresTaskRunStore) TaskRunByID(tenantID string, runID string) (TaskRun, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(
		ctx,
		taskRunSelectSQL+` WHERE tr.id = $1::uuid AND tr.tenant_id = $2::uuid LIMIT 1`,
		runID,
		tenantID,
	)
	if err != nil {
		return TaskRun{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return TaskRun{}, ErrNotFound
	}
	return scanTaskRun(rows)
}

// ListTaskRuns 按团队分页读取运行记录列表，统计字段按事件表实时计算。
// tenantID 为团队 ID，userEmail 为普通用户过滤邮箱，isAdmin 为管理员时不过滤。
func (s *PostgresTaskRunStore) ListTaskRuns(tenantID string, userEmail string, isAdmin bool, page int, pageSize int) (TaskRunListResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	page, pageSize = normalizeCandidatePage(page, pageSize)

	whereClause := ` WHERE tr.tenant_id = $1::uuid`
	args := []any{tenantID}
	if !isAdmin && strings.TrimSpace(userEmail) != "" {
		whereClause += ` AND u.email = $2`
		args = append(args, strings.TrimSpace(userEmail))
	}

	var total int
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM task_runs tr JOIN users u ON u.id = tr.user_id`+whereClause,
		args...,
	).Scan(&total); err != nil {
		return TaskRunListResult{}, err
	}

	// LIMIT 与 OFFSET 都用参数占位，避免参数编号出现空洞导致 PostgreSQL 报 42P18。
	query := taskRunSelectSQL + whereClause + ` ORDER BY tr.created_at DESC LIMIT $` + strconv.Itoa(len(args)+1) + ` OFFSET $` + strconv.Itoa(len(args)+2)
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return TaskRunListResult{}, err
	}
	defer rows.Close()
	items := make([]TaskRun, 0)
	for rows.Next() {
		run, err := scanTaskRun(rows)
		if err != nil {
			return TaskRunListResult{}, err
		}
		items = append(items, run)
	}
	if err := rows.Err(); err != nil {
		return TaskRunListResult{}, err
	}
	return TaskRunListResult{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

// TaskRunCandidates 按名单类型读取本次运行的候选人名单。
// tenantID 为团队 ID，runID 为运行 ID，filter 支持 greeted 和 resume。
func (s *PostgresTaskRunStore) TaskRunCandidates(tenantID string, runID string, filter string) ([]TaskRunCandidate, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	eventType := "greeted_sent"
	if strings.TrimSpace(filter) == "resume" {
		eventType = "resume_requested"
	}
	rows, err := s.db.QueryContext(
		ctx,
		`
		SELECT p.id, p.candidate_name, p.phone,
			detail.score, detail.reason,
			greet.score, greet.reason,
			e.created_at, e.message_text
		FROM candidate_events e
		JOIN candidate_profiles p ON p.id = e.candidate_id
		LEFT JOIN LATERAL (
			SELECT d.score, d.reason FROM candidate_events d
			WHERE d.task_id = e.task_id AND d.candidate_id = e.candidate_id AND d.event_type = 'detail_analysis'
			ORDER BY d.created_at DESC LIMIT 1
		) detail ON TRUE
		LEFT JOIN LATERAL (
			SELECT g.score, g.reason FROM candidate_events g
			WHERE g.task_id = e.task_id AND g.candidate_id = e.candidate_id AND g.event_type = 'greet_analysis'
			ORDER BY g.created_at DESC LIMIT 1
		) greet ON TRUE
		WHERE e.task_id = $1::uuid AND e.event_type = $2
			AND EXISTS (SELECT 1 FROM task_runs tr WHERE tr.id = e.task_id AND tr.tenant_id = $3::uuid)
		ORDER BY e.created_at DESC
		`,
		runID,
		eventType,
		tenantID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TaskRunCandidate, 0)
	for rows.Next() {
		var item TaskRunCandidate
		var actionAt sql.NullTime
		if err := rows.Scan(
			&item.CandidateID,
			&item.CandidateName,
			&item.Phone,
			&item.AIDetailScore,
			&item.AIDetailReason,
			&item.AIGreetScore,
			&item.AIGreetReason,
			&actionAt,
			&item.MessageText,
		); err != nil {
			return nil, err
		}
		if actionAt.Valid {
			item.ActionAt = &actionAt.Time
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// taskRunSelectSQL 运行记录查询的公共字段，包含岗位名、用户邮箱和按事件实时统计的数量。
const taskRunSelectSQL = `
	SELECT tr.id, u.email, COALESCE(tr.position_id::text,''), COALESCE(p.name,''), tr.platform_id,
		tr.task_type, tr.machine_id, tr.status,
		(SELECT COUNT(DISTINCT ce.candidate_id) FROM candidate_events ce WHERE ce.task_id = tr.id) AS scanned_count,
		(SELECT COUNT(*) FROM candidate_events ce WHERE ce.task_id = tr.id AND ce.event_type = 'greeted_sent') AS greeted_count,
		(SELECT COUNT(*) FROM candidate_events ce WHERE ce.task_id = tr.id AND ce.event_type = 'resume_requested') AS resume_requested_count,
		tr.skipped_count, tr.failed_count, tr.error_message,
		tr.created_at, tr.started_at, tr.finished_at
	FROM task_runs tr
	JOIN users u ON u.id = tr.user_id
	LEFT JOIN positions p ON p.id = tr.position_id`

// scanTaskRun 扫描一行运行记录。
// rows 为查询结果游标，返回一条运行记录。
func scanTaskRun(rows *sql.Rows) (TaskRun, error) {
	var run TaskRun
	var finishedAt sql.NullTime
	if err := rows.Scan(
		&run.ID,
		&run.UserEmail,
		&run.PositionID,
		&run.PositionName,
		&run.PlatformID,
		&run.TaskType,
		&run.MachineID,
		&run.Status,
		&run.ScannedCount,
		&run.GreetedCount,
		&run.ResumeRequestedCount,
		&run.SkippedCount,
		&run.FailedCount,
		&run.ErrorMessage,
		&run.CreatedAt,
		&run.StartedAt,
		&finishedAt,
	); err != nil {
		return TaskRun{}, err
	}
	if finishedAt.Valid {
		run.FinishedAt = &finishedAt.Time
	}
	return run, nil
}
