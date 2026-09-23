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
	ID                  string    `json:"id"`
	PositionID          string    `json:"position_id"`
	Platform            string    `json:"platform"`
	PlatformCandidateID string    `json:"platform_candidate_id"`
	CandidateName       string    `json:"candidate_name"`
	Score               int       `json:"score"`
	Status              string    `json:"status"`        // passed / screened
	ResumeStatus        string    `json:"resume_status"` // none / requested / received
	Source              string    `json:"source"`        // greeting / auto_reply
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
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
		`SELECT id, position_id, platform, platform_candidate_id, candidate_name, score, status, resume_status, source, created_at, updated_at
		 FROM candidate_screenings
		 WHERE position_id = $1 AND platform = $2 AND platform_candidate_id = $3`,
		positionID, platform, platformCandidateID,
	)
	var item CandidateScreening
	if err := row.Scan(&item.ID, &item.PositionID, &item.Platform, &item.PlatformCandidateID,
		&item.CandidateName, &item.Score, &item.Status, &item.ResumeStatus, &item.Source,
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
		`SELECT id, position_id, platform, platform_candidate_id, candidate_name, score, status, resume_status, source, created_at, updated_at
		 FROM candidate_screenings
		 WHERE position_id = $1 AND platform = $2 AND candidate_name = $3
		 ORDER BY updated_at DESC LIMIT 1`,
		positionID, platform, candidateName,
	)
	var item CandidateScreening
	if err := row.Scan(&item.ID, &item.PositionID, &item.Platform, &item.PlatformCandidateID,
		&item.CandidateName, &item.Score, &item.Status, &item.ResumeStatus, &item.Source,
		&item.CreatedAt, &item.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &item, nil
}

// UpsertScreening 插入或更新扫描记录。
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
	var saved CandidateScreening
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO candidate_screenings (position_id, platform, platform_candidate_id, candidate_name, score, status, resume_status, source)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (position_id, platform, platform_candidate_id)
		 DO UPDATE SET score = EXCLUDED.score, status = EXCLUDED.status,
		               resume_status = CASE WHEN EXCLUDED.resume_status != 'none' THEN EXCLUDED.resume_status ELSE candidate_screenings.resume_status END,
		               source = EXCLUDED.source, updated_at = now()
		 RETURNING id, position_id, platform, platform_candidate_id, candidate_name, score, status, resume_status, source, created_at, updated_at`,
		positionID, item.Platform, item.PlatformCandidateID, item.CandidateName, item.Score, status, resumeStatus, source,
	).Scan(&saved.ID, &saved.PositionID, &saved.Platform, &saved.PlatformCandidateID,
		&saved.CandidateName, &saved.Score, &saved.Status, &saved.ResumeStatus, &saved.Source,
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
		`SELECT id, position_id, platform, platform_candidate_id, candidate_name, score, status, resume_status, source, created_at, updated_at
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
