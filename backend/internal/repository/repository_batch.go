package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cygreenenv/greenhouse-panel/internal/constants"
	apperrors "github.com/cygreenenv/greenhouse-panel/internal/errors"
	"github.com/cygreenenv/greenhouse-panel/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"time"
)

type BatchRepository struct{ db *gorm.DB }

func NewBatchRepository(db *gorm.DB) *BatchRepository { return &BatchRepository{db: db} }

// SubmitDecision 由业务层在持有批次行锁时给出的处理决定，仓储在同一事务内落库。
type SubmitDecision struct {
	Status      string
	IssuesJSON  string
	Readings    []model.SensorReading
	SubmittedAt *time.Time
	ArchivedAt  *time.Time
	ClearActive bool
}

// SubmitDecider 在事务内根据锁定后的批次、条目与温室传感器计算校验结果。
type SubmitDecider func(batch *model.MeasurementBatch, entries []model.MeasurementEntry, sensors []model.Sensor) (*SubmitDecision, error)

func (r *BatchRepository) lockBatch(tx *gorm.DB, id uint) (*model.MeasurementBatch, error) {
	query := tx
	if r.db.Dialector.Name() == "mysql" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var batch model.MeasurementBatch
	err := query.First(&batch, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.ErrRecordNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock batch: %w", err)
	}
	return &batch, nil
}

func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "Duplicate entry") || strings.Contains(message, "UNIQUE constraint failed")
}

func encodeIssues(issues []model.BatchIssue) string {
	if len(issues) == 0 {
		return ""
	}
	raw, err := json.Marshal(issues)
	if err != nil {
		return ""
	}
	return string(raw)
}

func decodeIssues(batch *model.MeasurementBatch) {
	batch.Issues = nil
	if batch.IssuesJSON == "" {
		return
	}
	var issues []model.BatchIssue
	if err := json.Unmarshal([]byte(batch.IssuesJSON), &issues); err == nil {
		batch.Issues = issues
	}
}

func (r *BatchRepository) CreateBatch(batch *model.MeasurementBatch) error {
	if err := r.db.Create(batch).Error; err != nil {
		if isDuplicateKey(err) {
			return apperrors.ErrBatchOpen
		}
		return fmt.Errorf("create batch: %w", err)
	}
	return nil
}

func (r *BatchRepository) GetBatch(id uint) (*model.MeasurementBatch, error) {
	var batch model.MeasurementBatch
	err := r.db.Preload("Entries", func(db *gorm.DB) *gorm.DB { return db.Order("id asc") }).
		Preload("Entries.Sensor.Threshold").First(&batch, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.ErrRecordNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get batch: %w", err)
	}
	decodeIssues(&batch)
	return &batch, nil
}

func (r *BatchRepository) ListBatches(greenhouseID uint) ([]model.MeasurementBatch, error) {
	var rows []model.MeasurementBatch
	err := r.db.Where("greenhouse_id = ?", greenhouseID).
		Preload("Entries", func(db *gorm.DB) *gorm.DB { return db.Order("id asc") }).
		Preload("Entries.Sensor.Threshold").
		Order("id desc").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list batches: %w", err)
	}
	for i := range rows {
		decodeIssues(&rows[i])
	}
	return rows, nil
}

// AddEntry 在事务内锁定批次行，确认未归档后插入草稿；唯一索引冲突映射为重复录入错误，绝不覆盖已有条目。
func (r *BatchRepository) AddEntry(entry *model.MeasurementEntry) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		batch, err := r.lockBatch(tx, entry.BatchID)
		if err != nil {
			return err
		}
		if batch.Status == constants.BatchArchived {
			return apperrors.ErrBatchArchived
		}
		if err = tx.Create(entry).Error; err != nil {
			if isDuplicateKey(err) {
				return apperrors.ErrEntryDuplicate
			}
			return fmt.Errorf("create entry: %w", err)
		}
		return nil
	})
}

// UpdateEntryValue 修正草稿数值，仅允许未归档批次。
func (r *BatchRepository) UpdateEntryValue(batchID, entryID uint, value float64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		batch, err := r.lockBatch(tx, batchID)
		if err != nil {
			return err
		}
		if batch.Status == constants.BatchArchived {
			return apperrors.ErrBatchArchived
		}
		result := tx.Model(&model.MeasurementEntry{}).Where("id = ? AND batch_id = ?", entryID, batchID).Update("value", value)
		if result.Error != nil {
			return fmt.Errorf("update entry: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return apperrors.ErrRecordNotFound
		}
		return nil
	})
}

// DeleteEntry 移除草稿条目，仅允许未归档批次。
func (r *BatchRepository) DeleteEntry(batchID, entryID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		batch, err := r.lockBatch(tx, batchID)
		if err != nil {
			return err
		}
		if batch.Status == constants.BatchArchived {
			return apperrors.ErrBatchArchived
		}
		result := tx.Where("id = ? AND batch_id = ?", entryID, batchID).Delete(&model.MeasurementEntry{})
		if result.Error != nil {
			return fmt.Errorf("delete entry: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return apperrors.ErrRecordNotFound
		}
		return nil
	})
}

// Submit 在单事务内锁定批次、执行校验决定并原子落库：要么整批拒绝只更新状态，要么整批归档并写入正式读数。
func (r *BatchRepository) Submit(id uint, decide SubmitDecider) (*model.MeasurementBatch, error) {
	err := r.db.Transaction(func(tx *gorm.DB) error {
		batch, err := r.lockBatch(tx, id)
		if err != nil {
			return err
		}
		var entries []model.MeasurementEntry
		if err = tx.Where("batch_id = ?", batch.ID).Order("id asc").Find(&entries).Error; err != nil {
			return fmt.Errorf("load batch entries: %w", err)
		}
		var sensors []model.Sensor
		if err = tx.Preload("Threshold").Where("greenhouse_id = ?", batch.GreenhouseID).Find(&sensors).Error; err != nil {
			return fmt.Errorf("load batch sensors: %w", err)
		}
		decision, err := decide(batch, entries, sensors)
		if err != nil {
			return err
		}
		if len(decision.Readings) > 0 {
			if err = tx.Create(&decision.Readings).Error; err != nil {
				return fmt.Errorf("create archived readings: %w", err)
			}
		}
		active := batch.ActiveGreenhouseID
		if decision.ClearActive {
			active = nil
		}
		updates := map[string]any{"status": decision.Status, "issues": decision.IssuesJSON, "submitted_at": decision.SubmittedAt, "archived_at": decision.ArchivedAt, "active_greenhouse_id": active}
		if err = tx.Model(&model.MeasurementBatch{}).Where("id = ?", batch.ID).Updates(updates).Error; err != nil {
			return fmt.Errorf("update batch state: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r.GetBatch(id)
}

// EncodeIssues 供业务层序列化待修正项。
func EncodeIssues(issues []model.BatchIssue) string { return encodeIssues(issues) }
