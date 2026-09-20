package service

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cygreenenv/greenhouse-panel/internal/constants"
	apperrors "github.com/cygreenenv/greenhouse-panel/internal/errors"
	"github.com/cygreenenv/greenhouse-panel/internal/model"
	"github.com/cygreenenv/greenhouse-panel/internal/repository"
	ws "github.com/cygreenenv/greenhouse-panel/internal/websocket"
)

// MeasurementService 实现温室测量复核闭环：创建批次 → 逐项录入 → 校验 → 原子归档。
type MeasurementService struct {
	batchRepo  *repository.MeasurementRepository
	greenRepo  *repository.GreenhouseRepository
	sensorRepo *repository.SensorRepository
	logger     *slog.Logger
	hub        *ws.Hub
}

func NewMeasurementService(batch *repository.MeasurementRepository, green *repository.GreenhouseRepository, sensor *repository.SensorRepository, l *slog.Logger, h *ws.Hub) *MeasurementService {
	return &MeasurementService{batchRepo: batch, greenRepo: green, sensorRepo: sensor, logger: l, hub: h}
}

// CreateBatch 为温室创建复核批次。
func (s *MeasurementService) CreateBatch(greenhouseID uint) (*model.MeasurementBatch, error) {
	if _, err := s.greenRepo.Get(greenhouseID); err != nil {
		return nil, err
	}
	var batch *model.MeasurementBatch
	// 并发创建时批次号可能撞唯一索引，最多重试若干次重新取号。
	for attempt := 0; attempt < 3; attempt++ {
		now := time.Now()
		count, err := s.batchRepo.CountBatchesSince(now.Add(-24 * time.Hour))
		if err != nil {
			return nil, err
		}
		candidate := &model.MeasurementBatch{
			BatchNo:      fmt.Sprintf("%s%s-%03d", constants.BatchNoPrefix, now.Format(constants.BatchNoTimeLayout), count+1+int64(attempt)),
			GreenhouseID: greenhouseID,
			Status:       model.BatchStatusDraft,
			LastIssues:   model.BatchIssues{},
		}
		if err = s.batchRepo.CreateBatch(candidate); err != nil {
			if errors.Is(err, apperrors.ErrBatchNoConflict) {
				continue
			}
			return nil, err
		}
		batch = candidate
		break
	}
	if batch == nil {
		return nil, apperrors.ErrValidation
	}
	s.logger.Info("measurement batch created", "batchId", batch.ID, "batchNo", batch.BatchNo, "greenhouseId", greenhouseID)
	return s.batchRepo.GetBatch(batch.ID)
}

// ListBatches 总览批次列表。
func (s *MeasurementService) ListBatches(greenhouseID uint, status string) ([]model.MeasurementBatch, error) {
	return s.batchRepo.ListBatches(greenhouseID, status)
}

// GetBatch 批次详情（含录入项、传感器与阈值）。
func (s *MeasurementService) GetBatch(id uint) (*model.MeasurementBatch, error) {
	return s.batchRepo.GetBatch(id)
}

// AddEntry 向批次录入测量值。强制三条不变式：
// 1) 批次必须存在且处于草稿态；2) 传感器必须属于该温室；
// 3) 同一传感器一批仅一条，重复或并发录入冲突失败、绝不覆盖。
func (s *MeasurementService) AddEntry(batchID, sensorID uint, value float64) (*model.BatchEntry, error) {
	batch, err := s.loadDraftBatch(batchID)
	if err != nil {
		return nil, err
	}
	sensor, err := s.sensorRepo.Get(sensorID)
	if err != nil {
		return nil, err
	}
	if sensor.GreenhouseID != batch.GreenhouseID {
		return nil, apperrors.ErrSensorNotInGreenhouse
	}
	entry := &model.BatchEntry{BatchID: batchID, SensorID: sensorID, Value: value}
	if err = s.batchRepo.AddEntry(entry); err != nil {
		return nil, err
	}
	entry.Sensor = sensor
	s.hub.Broadcast(constants.EventBatchEntryCreated, entry)
	return entry, nil
}

// CorrectEntry 显式修正草稿批次中的既有录入值；未录入过的传感器返回校验错误。
func (s *MeasurementService) CorrectEntry(batchID, sensorID uint, value float64) (*model.BatchEntry, error) {
	batch, err := s.loadDraftBatch(batchID)
	if err != nil {
		return nil, err
	}
	sensor, err := s.sensorRepo.Get(sensorID)
	if err != nil {
		return nil, err
	}
	if sensor.GreenhouseID != batch.GreenhouseID {
		return nil, apperrors.ErrSensorNotInGreenhouse
	}
	found, err := s.batchRepo.UpdateEntry(batchID, sensorID, value)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, apperrors.ErrValidation
	}
	entry := &model.BatchEntry{BatchID: batchID, SensorID: sensorID, Value: value, Sensor: sensor}
	s.hub.Broadcast(constants.EventBatchEntryCreated, entry)
	return entry, nil
}

// Check 仅执行校验并返回待修正项（不改变批次状态），供逐项录入后实时提示。
func (s *MeasurementService) Check(batchID uint) (model.BatchIssues, error) {
	batch, err := s.batchRepo.GetBatch(batchID)
	if err != nil {
		return nil, err
	}
	greenhouse, err := s.greenRepo.Get(batch.GreenhouseID)
	if err != nil {
		return nil, err
	}
	issues := validateBatch(greenhouse.Sensors, batch.Entries)
	now := time.Now()
	if batch.Status == model.BatchStatusDraft {
		if err = s.batchRepo.SaveCheckResult(batchID, issues, now); err != nil {
			return nil, err
		}
	}
	return issues, nil
}

// Submit 提交复核：缺项、重复或超阈值则整批拒绝、不生成正式读数并返回待修正项；
// 全部齐全且合格时在单事务内原子归档并写入读数。归档后追加录入或重复提交直接失败。
func (s *MeasurementService) Submit(batchID uint) ([]model.SensorReading, *model.MeasurementBatch, error) {
	if _, err := s.batchRepo.GetBatch(batchID); err != nil {
		return nil, nil, err
	}
	now := time.Now()
	readings, issues, err := s.batchRepo.Submit(batchID, now, validateBatch)
	if err != nil {
		return nil, nil, err
	}
	batch, err := s.batchRepo.GetBatch(batchID)
	if err != nil {
		return nil, nil, err
	}
	if len(issues) > 0 {
		s.logger.Info("measurement batch rejected", "batchId", batchID, "issues", len(issues))
		return nil, batch, &apperrors.BatchRejectedError{BatchID: batchID, Issues: issues}
	}
	for i := range readings {
		s.hub.Broadcast("reading.created", readings[i])
	}
	s.hub.Broadcast(constants.EventBatchSubmitted, batch)
	s.logger.Info("measurement batch archived", "batchId", batchID, "readings", len(readings))
	return readings, batch, nil
}

// loadDraftBatch 取出批次并拒绝已归档批次的任何写操作。
func (s *MeasurementService) loadDraftBatch(batchID uint) (*model.MeasurementBatch, error) {
	batch, err := s.batchRepo.GetBatch(batchID)
	if err != nil {
		return nil, err
	}
	if batch.Status != model.BatchStatusDraft {
		return nil, apperrors.ErrBatchClosed
	}
	return batch, nil
}

// validateBatch 计算待修正项：缺项 / 重复 / 超阈值，顺序与消息保持稳定。
func validateBatch(sensors []model.Sensor, entries []model.BatchEntry) model.BatchIssues {
	issues := model.BatchIssues{}
	bySensor := make(map[uint][]model.BatchEntry, len(entries))
	for _, entry := range entries {
		bySensor[entry.SensorID] = append(bySensor[entry.SensorID], entry)
	}
	sensorByID := make(map[uint]model.Sensor, len(sensors))
	for _, sensor := range sensors {
		sensorByID[sensor.ID] = sensor
	}
	for _, sensor := range sensors {
		rows := bySensor[sensor.ID]
		switch {
		case len(rows) == 0:
			issues = append(issues, model.BatchIssue{
				SensorID: sensor.ID, SensorName: sensor.Name, Type: sensor.Type, Code: model.IssueMissing,
				Message: fmt.Sprintf("%s 缺少测量值，待补录", constants.SensorLabels[sensor.Type]),
			})
		case len(rows) > 1:
			issues = append(issues, model.BatchIssue{
				SensorID: sensor.ID, SensorName: sensor.Name, Type: sensor.Type, Code: model.IssueDuplicate,
				Message: fmt.Sprintf("%s 存在 %d 条录入，每传感器每批仅允许一条", constants.SensorLabels[sensor.Type], len(rows)),
			})
		default:
			value := rows[0].Value
			if value < sensor.Threshold.MinValue || value > sensor.Threshold.MaxValue {
				issues = append(issues, model.BatchIssue{
					SensorID: sensor.ID, SensorName: sensor.Name, Type: sensor.Type, Code: model.IssueThreshold,
					Value: value, MinValue: sensor.Threshold.MinValue, MaxValue: sensor.Threshold.MaxValue,
					Message: fmt.Sprintf("%s %.2f%s 超出阈值 [%.2f, %.2f]", constants.SensorLabels[sensor.Type], value, sensor.Unit, sensor.Threshold.MinValue, sensor.Threshold.MaxValue),
				})
			}
		}
	}
	for _, entry := range entries {
		// 录入了不属于该温室的传感器（理论上被 AddEntry 拦截，提交时再防御性兜底）。
		if _, ok := sensorByID[entry.SensorID]; !ok {
			issues = append(issues, model.BatchIssue{
				SensorID: entry.SensorID, Code: model.IssueDuplicate,
				Message: fmt.Sprintf("传感器 %d 不属于该温室", entry.SensorID),
			})
		}
	}
	return issues
}
