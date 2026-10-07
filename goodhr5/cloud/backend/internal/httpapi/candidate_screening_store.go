// 本文件提供候选人扫描记录的存储接口与 PostgreSQL 实现。
// 扫描记录是打招呼流程与自动回复流程的共享状态：打招呼写入评分 >= 50 的候选人，自动回复写入所有遇到的会话候选人。
package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// CandidateScreening 表示一条候选人扫描记录。
type CandidateScreening struct {
	ID                  string     `json:"id"`
	PositionID          string     `json:"position_id"`
	Platform            string     `json:"platform"`
	PlatformCandidateID string     `json:"platform_candidate_id"`
	CandidateName       string     `json:"candidate_name"`
	Score               int        `json:"score"`
	Status              string     `json:"status"`                       // passed / screened
	ResumeStatus        string     `json:"resume_status"`                // none / requested / received
	Source              string     `json:"source"`                       // greeting / auto_reply
	GreetedAt           *time.Time `json:"greeted_at,omitempty"`         // 首次打招呼成功时间，upsert 永不覆盖
	LastReGreetedAt     *time.Time `json:"last_re_greeted_at,omitempty"` // 上次复打时间
	ReGreetCount        int        `json:"re_greet_count"`               // 累计复打次数
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// CandidateScreeningUpsert 表示上报扫描记录时的输入字段。
type CandidateScreeningUpsert struct {
	Platform            string `json:"platform"`
	PlatformCandidateID string `json:"platform_candidate_id"`
	CandidateName       string `json:"candidate_name"`
	Score               int    `json:"score"`
	Status              string `json:"status"`
	ResumeStatus        string `json:"resume_status,omitempty"`
	Source              string `json:"source"`
	// SetGreetedAt 为 true 时写入 greeted_at = now()；为 false 时不写（upsert 冲突时保留原值）。
	// 打招呼流程（source='greeting' 且 status='passed'）上报时设为 true，其他流程保持 false。
	SetGreetedAt bool `json:"-"`
}

// CandidateScreeningStore 定义候选人扫描记录的读写能力。
type CandidateScreeningStore interface {
	// FindScreening 按岗位+平台+候选人标识查找扫描记录，找不到返回 ErrNotFound。
	FindScreening(ctx context.Context, positionID, platform, platformCandidateID string) (*CandidateScreening, error)
	// FindScreeningByName 按岗位+平台+候选人姓名查找扫描记录，找不到返回 ErrNotFound。
	FindScreeningByName(ctx context.Context, positionID, platform, candidateName string) (*CandidateScreening, error)
	// UpsertScreening 插入或更新扫描记录，同一岗位同一平台同一候选人只保留一条。
	UpsertScreening(ctx context.Context, item CandidateScreeningUpsert, positionID string) (*CandidateScreening, error)
	// ListScreeningsByPosition 按岗位分页返回扫描记录。
	ListScreeningsByPosition(ctx context.Context, positionID string, limit, offset int) ([]CandidateScreening, int, error)
	// MarkReGreetDone 批量更新 last_re_greeted_at=now() 并对 re_greet_count +1。
	// 复打招呼流程在成功发送后调用，云端据此维护复打计数与时间戳。
	MarkReGreetDone(ctx context.Context, positionID, platform string, platformCandidateIDs []string) (int64, error)
	// ListReGreetCandidates 查询复打招呼候选名单：
	// 同一岗位+平台下，greeted_at 不为空、在时间范围内、未超过复打次数上限，
	// 且距上次复打（或首次打招呼）已满间隔下限的候选人。
	ListReGreetCandidates(ctx context.Context, positionID, platform string, timeRangeDays, intervalMinMinutes, maxCount int) ([]CandidateScreening, error)
}

// PostgresCandidateScreeningStore 使用 PostgreSQL 持久化候选人扫描记录。
type PostgresCandidateScreeningStore struct {
	db *sql.DB
}

// NewPostgresCandidateScreeningStore 创建 PostgreSQL 候选人扫描记录存储。
func NewPostgresCandidateScreeningStore(db *sql.DB) *PostgresCandidateScreeningStore {
	return &PostgresCandidateScreeningStore{db: db}
}

// FindScreening 按岗位+平台+候选人标识查找扫描记录。
func (s *PostgresCandidateScreeningStore) FindScreening(ctx context.Context, positionID, platform, platformCandidateID string) (*CandidateScreening, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, position_id, platform, platform_candidate_id, candidate_name, score, status, resume_status, source,
		       greeted_at, last_re_greeted_at, re_greet_count, created_at, updated_at
		 FROM candidate_screenings
		 WHERE position_id = $1 AND platform = $2 AND platform_candidate_id = $3`,
		positionID, platform, platformCandidateID,
	)
	var item CandidateScreening
	if err := row.Scan(&item.ID, &item.PositionID, &item.Platform, &item.PlatformCandidateID,
		&item.CandidateName, &item.Score, &item.Status, &item.ResumeStatus, &item.Source,
		&item.GreetedAt, &item.LastReGreetedAt, &item.ReGreetCount,
		&item.CreatedAt, &item.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &item, nil
}

// FindScreeningByName 按岗位+平台+候选人姓名查找最近一条扫描记录。
func (s *PostgresCandidateScreeningStore) FindScreeningByName(ctx context.Context, positionID, platform, candidateName string) (*CandidateScreening, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, position_id, platform, platform_candidate_id, candidate_name, score, status, resume_status, source,
		       greeted_at, last_re_greeted_at, re_greet_count, created_at, updated_at
		 FROM candidate_screenings
		 WHERE position_id = $1 AND platform = $2 AND candidate_name = $3
		 ORDER BY updated_at DESC LIMIT 1`,
		positionID, platform, candidateName,
	)
	var item CandidateScreening
	if err := row.Scan(&item.ID, &item.PositionID, &item.Platform, &item.PlatformCandidateID,
		&item.CandidateName, &item.Score, &item.Status, &item.ResumeStatus, &item.Source,
		&item.GreetedAt, &item.LastReGreetedAt, &item.ReGreetCount,
		&item.CreatedAt, &item.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &item, nil
}

// UpsertScreening 插入或更新扫描记录。
// greeted_at 仅在 SetGreetedAt=true 时写入；冲突更新时用 COALESCE 保留原值，永不覆盖（见复打招呼开发清单 R-09）。
func (s *PostgresCandidateScreeningStore) UpsertScreening(ctx context.Context, item CandidateScreeningUpsert, positionID string) (*CandidateScreening, error) {
	status := strings.TrimSpace(item.Status)
	if status == "" {
		status = "screened"
	}
	resumeStatus := strings.TrimSpace(item.ResumeStatus)
	if resumeStatus == "" {
		resumeStatus = "none"
	}
	source := strings.TrimSpace(item.Source)
	if source == "" {
		source = "greeting"
	}
	var greetedAt any
	if item.SetGreetedAt {
		greetedAt = time.Now()
	}
	var saved CandidateScreening
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO candidate_screenings (position_id, platform, platform_candidate_id, candidate_name, score, status, resume_status, source, greeted_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (position_id, platform, platform_candidate_id)
		 DO UPDATE SET score = EXCLUDED.score, status = EXCLUDED.status,
		               resume_status = CASE WHEN EXCLUDED.resume_status != 'none' THEN EXCLUDED.resume_status ELSE candidate_screenings.resume_status END,
		               source = EXCLUDED.source,
		               greeted_at = COALESCE(candidate_screenings.greeted_at, EXCLUDED.greeted_at),
		               updated_at = now()
		 RETURNING id, position_id, platform, platform_candidate_id, candidate_name, score, status, resume_status, source,
		           greeted_at, last_re_greeted_at, re_greet_count, created_at, updated_at`,
		positionID, item.Platform, item.PlatformCandidateID, item.CandidateName, item.Score, status, resumeStatus, source, greetedAt,
	).Scan(&saved.ID, &saved.PositionID, &saved.Platform, &saved.PlatformCandidateID,
		&saved.CandidateName, &saved.Score, &saved.Status, &saved.ResumeStatus, &saved.Source,
		&saved.GreetedAt, &saved.LastReGreetedAt, &saved.ReGreetCount,
		&saved.CreatedAt, &saved.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

// ListScreeningsByPosition 按岗位分页返回扫描记录。
func (s *PostgresCandidateScreeningStore) ListScreeningsByPosition(ctx context.Context, positionID string, limit, offset int) ([]CandidateScreening, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM candidate_screenings WHERE position_id = $1`, positionID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, position_id, platform, platform_candidate_id, candidate_name, score, status, resume_status, source,
		       greeted_at, last_re_greeted_at, re_greet_count, created_at, updated_at
		 FROM candidate_screenings
		 WHERE position_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`,
		positionID, limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var items []CandidateScreening
	for rows.Next() {
		var item CandidateScreening
		if err := rows.Scan(&item.ID, &item.PositionID, &item.Platform, &item.PlatformCandidateID,
			&item.CandidateName, &item.Score, &item.Status, &item.ResumeStatus, &item.Source,
			&item.GreetedAt, &item.LastReGreetedAt, &item.ReGreetCount,
			&item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if items == nil {
		items = []CandidateScreening{}
	}
	return items, total, nil
}

// MarkReGreetDone 批量更新 last_re_greeted_at=now() 并对 re_greet_count +1。
func (s *PostgresCandidateScreeningStore) MarkReGreetDone(ctx context.Context, positionID, platform string, platformCandidateIDs []string) (int64, error) {
	if len(platformCandidateIDs) == 0 {
		return 0, nil
	}
	args := []any{positionID, platform, time.Now()}
	placeholders := make([]string, 0, len(platformCandidateIDs))
	for i, id := range platformCandidateIDs {
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+4))
		args = append(args, id)
	}
	query := `UPDATE candidate_screenings
		SET last_re_greeted_at = $3,
		    re_greet_count = COALESCE(re_greet_count, 0) + 1,
		    updated_at = now()
		WHERE position_id = $1 AND platform = $2 AND platform_candidate_id IN (` + strings.Join(placeholders, ",") + `)`
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ListReGreetCandidates 查询复打招呼候选名单：
//   greeted_at 不为空、在时间范围内、未超过复打次数上限，
//   且距上次复打（或首次打招呼）已满间隔下限的候选人。
func (s *PostgresCandidateScreeningStore) ListReGreetCandidates(ctx context.Context, positionID, platform string, timeRangeDays, intervalMinMinutes, maxCount int) ([]CandidateScreening, error) {
	now := time.Now()
	timeRangeStart := now.AddDate(0, 0, -timeRangeDays)
	intervalThreshold := now.Add(-time.Duration(intervalMinMinutes) * time.Minute)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, position_id, platform, platform_candidate_id, candidate_name, score, status, resume_status, source,
		       greeted_at, last_re_greeted_at, re_greet_count, created_at, updated_at
		 FROM candidate_screenings
		 WHERE position_id = $1 AND platform = $2
		   AND greeted_at IS NOT NULL
		   AND greeted_at >= $3
		   AND COALESCE(resume_status, 'none') <> 'received'
		   AND COALESCE(re_greet_count, 0) < $4
		   AND COALESCE(last_re_greeted_at, greeted_at) <= $5
		 ORDER BY greeted_at ASC`,
		positionID, platform, timeRangeStart, maxCount, intervalThreshold,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []CandidateScreening
	for rows.Next() {
		var item CandidateScreening
		if err := rows.Scan(&item.ID, &item.PositionID, &item.Platform, &item.PlatformCandidateID,
			&item.CandidateName, &item.Score, &item.Status, &item.ResumeStatus, &item.Source,
			&item.GreetedAt, &item.LastReGreetedAt, &item.ReGreetCount,
			&item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if items == nil {
		items = []CandidateScreening{}
	}
	return items, nil
}

// positionSubresourceScreeningID 从路径中提取岗位 ID，用于扫描记录路由。
func positionSubresourceScreeningID(path string) string {
	return positionSubresourceID(path, "screenings")
}

// validateScreeningUpsert 校验单条上报记录的必填字段。
func validateScreeningUpsert(item CandidateScreeningUpsert) error {
	if strings.TrimSpace(item.Platform) == "" {
		return fmt.Errorf("platform 不能为空")
	}
	if strings.TrimSpace(item.PlatformCandidateID) == "" {
		return fmt.Errorf("platform_candidate_id 不能为空")
	}
	return nil
}

// MemoryCandidateScreeningStore 提供开发期候选人扫描记录内存存储。
type MemoryCandidateScreeningStore struct {
	mu     sync.Mutex
	items  map[string]*CandidateScreening // key: positionID|platform|candidateID
	nextID int
}

// NewMemoryCandidateScreeningStore 创建候选人扫描记录内存存储。
func NewMemoryCandidateScreeningStore() *MemoryCandidateScreeningStore {
	return &MemoryCandidateScreeningStore{items: make(map[string]*CandidateScreening)}
}

// FindScreening 按岗位+平台+候选人标识查找扫描记录。
func (s *MemoryCandidateScreeningStore) FindScreening(_ context.Context, positionID, platform, platformCandidateID string) (*CandidateScreening, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := positionID + "|" + platform + "|" + platformCandidateID
	item, ok := s.items[key]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *item
	return &cp, nil
}

// FindScreeningByName 按岗位+平台+候选人姓名查找最近一条扫描记录。
func (s *MemoryCandidateScreeningStore) FindScreeningByName(_ context.Context, positionID, platform, candidateName string) (*CandidateScreening, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best *CandidateScreening
	for _, item := range s.items {
		if item.PositionID == positionID && item.Platform == platform && item.CandidateName == candidateName {
			if best == nil || item.UpdatedAt.After(best.UpdatedAt) {
				cp := *item
				best = &cp
			}
		}
	}
	if best == nil {
		return nil, ErrNotFound
	}
	return best, nil
}

// UpsertScreening 插入或更新扫描记录。
// 内存版：SetGreetedAt=true 时写入 greeted_at；冲突时仅当原值为空才写入（与 Postgres COALESCE 语义一致）。
func (s *MemoryCandidateScreeningStore) UpsertScreening(_ context.Context, item CandidateScreeningUpsert, positionID string) (*CandidateScreening, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := positionID + "|" + item.Platform + "|" + item.PlatformCandidateID
	now := time.Now().UTC()
	if existing, ok := s.items[key]; ok {
		existing.Score = item.Score
		if item.Status != "" {
			existing.Status = item.Status
		}
		if item.ResumeStatus != "" && item.ResumeStatus != "none" {
			existing.ResumeStatus = item.ResumeStatus
		}
		if item.Source != "" {
			existing.Source = item.Source
		}
		if item.SetGreetedAt && existing.GreetedAt == nil {
			t := now
			existing.GreetedAt = &t
		}
		existing.CandidateName = item.CandidateName
		existing.UpdatedAt = now
		cp := *existing
		return &cp, nil
	}
	s.nextID++
	status := item.Status
	if status == "" {
		status = "screened"
	}
	resumeStatus := item.ResumeStatus
	if resumeStatus == "" {
		resumeStatus = "none"
	}
	source := item.Source
	if source == "" {
		source = "greeting"
	}
	var greetedAt *time.Time
	if item.SetGreetedAt {
		t := now
		greetedAt = &t
	}
	saved := &CandidateScreening{
		ID:                  fmt.Sprintf("mem-screening-%d", s.nextID),
		PositionID:          positionID,
		Platform:            item.Platform,
		PlatformCandidateID: item.PlatformCandidateID,
		CandidateName:       item.CandidateName,
		Score:               item.Score,
		Status:              status,
		ResumeStatus:        resumeStatus,
		Source:              source,
		GreetedAt:           greetedAt,
		ReGreetCount:        0,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	s.items[key] = saved
	cp := *saved
	return &cp, nil
}

// ListScreeningsByPosition 按岗位分页返回扫描记录。
func (s *MemoryCandidateScreeningStore) ListScreeningsByPosition(_ context.Context, positionID string, limit, offset int) ([]CandidateScreening, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var matched []CandidateScreening
	for _, item := range s.items {
		if item.PositionID == positionID {
			matched = append(matched, *item)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].CreatedAt.After(matched[j].CreatedAt)
	})
	total := len(matched)
	if offset >= total {
		return []CandidateScreening{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return matched[offset:end], total, nil
}

// MarkReGreetDone 批量更新 last_re_greeted_at=now() 并对 re_greet_count +1（内存版）。
func (s *MemoryCandidateScreeningStore) MarkReGreetDone(_ context.Context, positionID, platform string, platformCandidateIDs []string) (int64, error) {
	if len(platformCandidateIDs) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	set := make(map[string]struct{}, len(platformCandidateIDs))
	for _, id := range platformCandidateIDs {
		set[id] = struct{}{}
	}
	now := time.Now().UTC()
	var affected int64
	for _, item := range s.items {
		if item.PositionID == positionID && item.Platform == platform {
			if _, ok := set[item.PlatformCandidateID]; ok {
				item.LastReGreetedAt = &now
				item.ReGreetCount++
				item.UpdatedAt = now
				affected++
			}
		}
	}
	return affected, nil
}

// ListReGreetCandidates 查询复打招呼候选名单（内存版）。
func (s *MemoryCandidateScreeningStore) ListReGreetCandidates(_ context.Context, positionID, platform string, timeRangeDays, intervalMinMinutes, maxCount int) ([]CandidateScreening, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	timeRangeStart := now.AddDate(0, 0, -timeRangeDays)
	intervalThreshold := now.Add(-time.Duration(intervalMinMinutes) * time.Minute)
	var items []CandidateScreening
	for _, item := range s.items {
		if item.PositionID != positionID || item.Platform != platform {
			continue
		}
		if item.GreetedAt == nil {
			continue
		}
		if item.ResumeStatus == "received" {
			continue
		}
		if item.GreetedAt.Before(timeRangeStart) {
			continue
		}
		if item.ReGreetCount >= maxCount {
			continue
		}
		lastTime := item.GreetedAt
		if item.LastReGreetedAt != nil {
			lastTime = item.LastReGreetedAt
		}
		if lastTime.After(intervalThreshold) {
			continue
		}
		items = append(items, *item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].GreetedAt.Before(*items[j].GreetedAt)
	})
	if items == nil {
		items = []CandidateScreening{}
	}
	return items, nil
}
