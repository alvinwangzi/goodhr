// 本文件从 HRPlus 原运行、M1 检查点与实际索要归属构建报告，不使用岗位累计量反推本次结果。
package localdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"goodhr5/local-agent-go/internal/planmodel"
	"time"
)

// BuildPlanReport 创建原运行结束摘要，待补传保留明确分类，尚未执行项不伪造检查点。
func (db *DB) BuildPlanReport(ctx context.Context, scope string, run planmodel.Run, generated time.Time, enabled bool) (planmodel.Report, error) {
	if err := run.Validate(); err != nil {
		return planmodel.Report{}, err
	}
	kind := ""
	switch run.State {
	case "completed":
		kind = "completed"
	case "incomplete":
		kind = "day_incomplete"
	case "stopped":
		kind = "stopped"
	case "blocked":
		kind = "failed"
	default:
		return planmodel.Report{}, fmt.Errorf("运行尚未结束，不能生成结束报告")
	}
	if scope == "" || generated.IsZero() {
		return planmodel.Report{}, ErrPlanRequestConflict
	}
	pending, err := db.PlanRecoveryPending(ctx, scope, run.ID)
	if err != nil {
		return planmodel.Report{}, err
	}
	report := planmodel.Report{SchemaVersion: 1, RunID: run.ID, PlanID: run.PlanID, ActivationID: run.ActivationID, ConfigVersion: run.ConfigVersion, RunSequence: run.Sequence, ExecutionDate: run.ExecutionDate, PlanName: run.Snapshot.Name, Kind: kind, RunState: run.State, EndReason: run.EndReason, SyncState: "confirmed", GeneratedAt: generated.UTC(), FinishedAt: run.FinishedAt, Items: []planmodel.ReportItem{}, UnfinishedItemIDs: []string{}}
	if pending {
		report.SyncState = "pending"
	}
	if run.FinishedAt != nil {
		finished := run.FinishedAt.UTC()
		report.FinishedAt = &finished
	}
	if enabled && kind != "stopped" {
		loc, _ := time.LoadLocation(run.Snapshot.Schedule.Timezone)
		endOfRunDate, err := time.ParseInLocation("2006-01-02", run.ExecutionDate, loc)
		if err != nil {
			return report, err
		}
		minimum := endOfRunDate.AddDate(0, 0, 1).Add(-time.Nanosecond)
		if generated.After(minimum) {
			minimum = generated
		}
		report.NextNominalAt, err = run.Snapshot.Schedule.NextStart(minimum)
		if err != nil {
			return report, err
		}
	}
	for _, item := range run.Items {
		row := planmodel.ReportItem{ID: item.ID, ItemID: item.ItemID, TaskRunID: item.TaskRunID, PositionID: item.Snapshot.PositionID, Order: item.Order, State: item.State, Actions: map[string]planmodel.ReportAction{}, Information: map[string]planmodel.ReportAction{}}
		cp, err := db.LoadActionCheckpoint(ctx, item.ID)
		hasCP := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return report, err
		}
		if hasCP && (cp.PlanRunID != run.ID || cp.ItemRunID != item.ID || cp.OwnerScope != scope || cp.CloudRunID != item.TaskRunID || cp.PositionID != item.Snapshot.PositionID) {
			return report, ErrPlanRequestConflict
		}
		row.DetailsAvailable = hasCP || item.TaskRunID == ""
		if hasCP {
			row.Scanned = int64(cp.Scanned)
		}
		unfinished := item.State != "completed"
		for action, progress := range item.Actions {
			value := planmodel.ReportAction{State: progress.State, Confirmed: progress.Count, Unknown: progress.UnknownCount}
			if hasCP {
				count, unknown, skipped, failed := 0, 0, 0, 0
				switch action {
				case "greeting":
					count, skipped, failed = cp.Greeted, cp.Skipped, cp.Failed
					states, err := db.ActionCandidateStates(ctx, item.ID, item.Snapshot.PositionID)
					if err != nil {
						return report, err
					}
					for _, state := range states {
						if state == "unknown" || state == "processing" {
							unknown++
						}
					}
				case "auto_reply":
					count, unknown, skipped, failed = cp.Replied, cp.ReplyStats["unknown"], cp.ReplyStats["skipped"], cp.ReplyStats["failed"]
				case "re_greet":
					count, unknown, skipped, failed = cp.ReGreeted, cp.ReGreetStats["unknown"], cp.ReGreetStats["skipped"], cp.ReGreetStats["failed"]
				}
				if count < 0 || unknown < 0 || skipped < 0 || failed < 0 || cp.Scanned < 0 {
					return report, ErrPlanRequestConflict
				}
				if int64(count) > value.Confirmed {
					value.Confirmed = int64(count)
				}
				if int64(unknown) > value.Unknown {
					value.Unknown = int64(unknown)
				}
				value.Skipped, value.Failed = int64(skipped), int64(failed)
			}
			row.Actions[action] = value
			if progress.State != "completed" || value.Unknown > 0 || value.Failed > 0 {
				unfinished = true
			}
		}
		facts, err := db.InfoAttributions(ctx, item.ID)
		if err != nil {
			return report, err
		}
		for _, fact := range facts {
			if fact.PlanRunID != run.ID || fact.ItemRunID != item.ID || fact.CloudRunID != item.TaskRunID || fact.OwnerScope != scope {
				return report, ErrPlanRequestConflict
			}
			value := row.Information[fact.Action]
			if fact.State == "requested" || fact.State == "satisfied" {
				value.Confirmed++
				if value.State == "" {
					value.State = "completed"
				}
			} else {
				value.Unknown++
				value.State = "pending_check"
				unfinished = true
			}
			row.Information[fact.Action] = value
		}
		report.Items = append(report.Items, row)
		if unfinished {
			report.UnfinishedItemIDs = append(report.UnfinishedItemIDs, item.ID)
		}
	}
	return report, nil
}
