package repository

import (
	"errors"
	"fmt"
	"strings"
	"time"

	apperrors "github.com/cygreenenv/greenhouse-panel/internal/errors"
	"github.com/cygreenenv/greenhouse-panel/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BatchValidationFunc 在提交事务内执行整批校验，返回待修正项。
// 注入函数让事务归仓储管理，业务规则仍归服务层。
type BatchValidationFunc func(sensors []model.Sensor, entries []model.BatchEntry) model.BatchIssues

type MeasurementRepository struct{ db *gorm.DB }

func NewMeasurementRepository(db *gorm.DB) *MeasurementRepository {
	return &MeasurementRepository{db: db}
}

// CreateBatch 持久化新批次。
func (r *MeasurementRepository) CreateBatch(batch *model.MeasurementBatch) error {
	err := r.db.Create(batch).Error
	if isDuplicateKey(err) {
		return apperrors.ErrBatchNoConflict
	}
	if err != nil {
		return fmt.Errorf("create measurement batch: %w", err)
	}
	return nil
}

// CountBatchesSince 统计指定时间之后的批次数量，用于生成批次号。
func (r *MeasurementRepository) CountBatchesSince(since time.Time) (int64, error) {
	var count int64
	if err := r.db.Model(&model.MeasurementBatch{}).Where("created_at >= ?", since).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count measurement batches: %w", err)
	}
	return count, nil
}

// ListBatches 按温室与状态列出批次，最新在前。
func (r *MeasurementRepository) ListBatches(greenhouseID uint, status string) ([]model.MeasurementBatch, error) {
	q := r.db.Preload("Greenhouse").Preload("Entries.Sensor.Threshold").Order("id desc")
	if greenhouseID != 0 {
		q = q.Where("greenhouse_id = ?", greenhouseID)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var rows []model.MeasurementBatch
	if err := q.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list measurement batches: %w", err)
	}
	return rows, nil
}

// GetBatch 读取批次及其录入项与传感器阈值。
func (r *MeasurementRepository) GetBatch(id uint) (*model.MeasurementBatch, error) {
	var batch model.MeasurementBatch
	err := r.db.Preload("Greenhouse").Preload("Entries.Sensor.Threshold").First(&batch, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.ErrRecordNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get measurement batch: %w", err)
	}
	return &batch, nil
}

// AddEntry 新建一条录入。(batch_id, sensor_id) 唯一索引冲突时返回 ErrEntryConflict，
// 因此重复或并发录入只会失败，不会覆盖既有值。
func (r *MeasurementRepository) AddEntry(entry *model.BatchEntry) error {
	err := r.db.Create(entry).Error
	if isDuplicateKey(err) {
		return apperrors.ErrEntryConflict
	}
	if err != nil {
		return fmt.Errorf("add batch entry: %w", err)
	}
	return nil
}

// UpdateEntry 修正草稿批次中的既有录入值（显式修正，区别于追加录入）。
// 返回 false 表示该传感器尚未录入，需走 AddEntry。
func (r *MeasurementRepository) UpdateEntry(batchID, sensorID uint, value float64) (bool, error) {
	result := r.db.Model(&model.BatchEntry{}).
		Where("batch_id = ? AND sensor_id = ?", batchID, sensorID).
		Update("value", value)
	if result.Error != nil {
		return false, fmt.Errorf("update batch entry: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

// SaveCheckResult 记录最近一次校验的待修正项快照，供刷新后展示失败原因。
func (r *MeasurementRepository) SaveCheckResult(batchID uint, issues model.BatchIssues, checkedAt time.Time) error {
	err := r.db.Model(&model.MeasurementBatch{}).Where("id = ? AND status = ?", batchID, model.BatchStatusDraft).
		Updates(map[string]any{"last_issues": issues, "last_checked_at": checkedAt}).Error
	if err != nil {
		return fmt.Errorf("save batch check result: %w", err)
	}
	return nil
}

// Submit 在单个数据库事务内完成“校验 + 归档 + 写入正式读数”。
// 校验发现待修正项时仅落盘校验快照并提交，不生成任何读数；
// 全部合格时原子地把批次置为归档并写入读数。并发/重复提交由行锁与状态条件拒绝。
// 返回 issues 非空表示整批被业务校验拒绝。
func (r *MeasurementRepository) Submit(batchID uint, recordedAt time.Time, validate BatchValidationFunc) ([]model.SensorReading, model.BatchIssues, error) {
	var readings []model.SensorReading
	var issues model.BatchIssues
	err := r.db.Transaction(func(tx *gorm.DB) error {
		batch, err := r.getBatchForUpdate(tx, batchID)
		if err != nil {
			return err
		}
		if batch.Status != model.BatchStatusDraft {
			return apperrors.ErrBatchClosed
		}
		sensors, err := r.sensorsForUpdate(tx, batch.GreenhouseID)
		if err != nil {
			return err
		}
		entries, err := r.entriesForUpdate(tx, batchID)
		if err != nil {
			return err
		}
		issues = validate(sensors, entries)
		if len(issues) > 0 {
			// 整批拒绝：仅保存待修正项，不写读数，批次仍为草稿。
			if err := tx.Model(&model.MeasurementBatch{}).Where("id = ?", batchID).
				Updates(map[string]any{"last_issues": issues, "last_checked_at": recordedAt}).Error; err != nil {
				return fmt.Errorf("persist batch issues: %w", err)
			}
			return nil
		}
		for _, entry := range entries {
			readings = append(readings, model.SensorReading{SensorID: entry.SensorID, Value: entry.Value, RecordedAt: recordedAt})
		}
		if err := tx.Create(&readings).Error; err != nil {
			return fmt.Errorf("create formal readings: %w", err)
		}
		archived := model.BatchStatusArchived
		result := tx.Model(&model.MeasurementBatch{}).
			Where("id = ? AND status = ?", batchID, model.BatchStatusDraft).
			Updates(map[string]any{"status": archived, "submitted_at": recordedAt, "last_checked_at": recordedAt, "last_issues": model.BatchIssues{}})
		if result.Error != nil {
			return fmt.Errorf("archive batch: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			// 并发提交抢先归档，回滚本事务全部读数。
			return apperrors.ErrBatchClosed
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return readings, issues, nil
}

func (r *MeasurementRepository) getBatchForUpdate(tx *gorm.DB, id uint) (*model.MeasurementBatch, error) {
	var batch model.MeasurementBatch
	q := tx
	if r.db.Dialector.Name() != "sqlite" {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := q.First(&batch, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.ErrBatchNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock measurement batch: %w", err)
	}
	return &batch, nil
}

func (r *MeasurementRepository) sensorsForUpdate(tx *gorm.DB, greenhouseID uint) ([]model.Sensor, error) {
	var sensors []model.Sensor
	q := tx.Where("greenhouse_id = ?", greenhouseID).Order("id asc")
	if r.db.Dialector.Name() != "sqlite" {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := q.Preload("Threshold").Find(&sensors).Error; err != nil {
		return nil, fmt.Errorf("lock greenhouse sensors: %w", err)
	}
	return sensors, nil
}

func (r *MeasurementRepository) entriesForUpdate(tx *gorm.DB, batchID uint) ([]model.BatchEntry, error) {
	var entries []model.BatchEntry
	q := tx.Where("batch_id = ?", batchID).Order("sensor_id asc")
	if r.db.Dialector.Name() != "sqlite" {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := q.Preload("Sensor.Threshold").Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("lock batch entries: %w", err)
	}
	return entries, nil
}

// isDuplicateKey 识别 MySQL(1062) 与 SQLite(UNIQUE constraint) 的唯一索引冲突。
func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate entry") || strings.Contains(message, "unique constraint failed")
}
