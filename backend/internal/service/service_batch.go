package service

import (
	"fmt"
	"github.com/cygreenenv/greenhouse-panel/internal/constants"
	apperrors "github.com/cygreenenv/greenhouse-panel/internal/errors"
	"github.com/cygreenenv/greenhouse-panel/internal/model"
	"github.com/cygreenenv/greenhouse-panel/internal/repository"
	ws "github.com/cygreenenv/greenhouse-panel/internal/websocket"
	"log/slog"
	"time"
)

type BatchService struct {
	batches     *repository.BatchRepository
	sensors     *repository.SensorRepository
	greenhouses *repository.GreenhouseRepository
	logger      *slog.Logger
	hub         *ws.Hub
}

func NewBatchService(b *repository.BatchRepository, s *repository.SensorRepository, g *repository.GreenhouseRepository, l *slog.Logger, h *ws.Hub) *BatchService {
	return &BatchService{b, s, g, l, h}
}

// Create 为温室开启一个测量复核批次；同一温室同时只允许一个未归档批次。
func (s *BatchService) Create(greenhouseID uint) (*model.MeasurementBatch, error) {
	if _, err := s.greenhouses.Get(greenhouseID); err != nil {
		return nil, err
	}
	batch := &model.MeasurementBatch{GreenhouseID: greenhouseID, Status: constants.BatchPending, ActiveGreenhouseID: &greenhouseID}
	if err := s.batches.CreateBatch(batch); err != nil {
		return nil, err
	}
	s.logger.Info("measurement batch created", "batch_id", batch.ID, "greenhouse_id", greenhouseID)
	return batch, nil
}

func (s *BatchService) List(greenhouseID uint) ([]model.MeasurementBatch, error) {
	return s.batches.ListBatches(greenhouseID)
}

func (s *BatchService) Detail(batchID uint) (*model.MeasurementBatch, error) {
	return s.batches.GetBatch(batchID)
}

// AddEntry 逐项录入草稿：仅允许本温室传感器，一批一传感器仅一条，重复或并发录入不得覆盖。
func (s *BatchService) AddEntry(batchID, sensorID uint, value float64) (*model.MeasurementBatch, error) {
	batch, err := s.batches.GetBatch(batchID)
	if err != nil {
		return nil, err
	}
	sensor, err := s.sensors.Get(sensorID)
	if err != nil {
		return nil, err
	}
	if sensor.GreenhouseID != batch.GreenhouseID {
		return nil, apperrors.ErrSensorMismatch
	}
	entry := &model.MeasurementEntry{BatchID: batchID, SensorID: sensorID, Value: value}
	if err = s.batches.AddEntry(entry); err != nil {
		return nil, err
	}
	s.logger.Info("batch entry added", "batch_id", batchID, "sensor_id", sensorID, "value", value)
	return s.batches.GetBatch(batchID)
}

// UpdateEntry 修正草稿数值（例如复核失败后调整越限值）。
func (s *BatchService) UpdateEntry(batchID, entryID uint, value float64) (*model.MeasurementBatch, error) {
	if err := s.batches.UpdateEntryValue(batchID, entryID, value); err != nil {
		return nil, err
	}
	return s.batches.GetBatch(batchID)
}

// DeleteEntry 移除误录的草稿条目。
func (s *BatchService) DeleteEntry(batchID, entryID uint) (*model.MeasurementBatch, error) {
	if err := s.batches.DeleteEntry(batchID, entryID); err != nil {
		return nil, err
	}
	return s.batches.GetBatch(batchID)
}

// Submit 提交复核：缺项、重复或越限任一项存在即整批拒绝并返回待修正项；全部合格则原子归档并写入正式读数。
func (s *BatchService) Submit(batchID uint) (*model.MeasurementBatch, error) {
	now := time.Now()
	batch, err := s.batches.Submit(batchID, func(locked *model.MeasurementBatch, entries []model.MeasurementEntry, sensors []model.Sensor) (*repository.SubmitDecision, error) {
		if locked.Status == constants.BatchArchived {
			return nil, apperrors.ErrBatchArchived
		}
		issues := validateBatch(entries, sensors)
		if len(issues) > 0 {
			return &repository.SubmitDecision{Status: constants.BatchRejected, IssuesJSON: repository.EncodeIssues(issues), SubmittedAt: &now}, nil
		}
		readings := make([]model.SensorReading, 0, len(entries))
		for _, entry := range entries {
			readings = append(readings, model.SensorReading{SensorID: entry.SensorID, Value: entry.Value, RecordedAt: now})
		}
		return &repository.SubmitDecision{Status: constants.BatchArchived, IssuesJSON: "", Readings: readings, SubmittedAt: &now, ArchivedAt: &now, ClearActive: true}, nil
	})
	if err != nil {
		return nil, err
	}
	if batch.Status == constants.BatchRejected {
		s.logger.Warn("measurement batch rejected", "batch_id", batch.ID, "issues", len(batch.Issues))
		return batch, apperrors.ErrBatchRejected
	}
	s.logger.Info("measurement batch archived", "batch_id", batch.ID, "readings", len(batch.Entries))
	s.hub.Broadcast(constants.EventBatch, batch)
	return batch, nil
}

// validateBatch 纯函数校验：缺项、重复、越限、非本温室传感器。
func validateBatch(entries []model.MeasurementEntry, sensors []model.Sensor) []model.BatchIssue {
	sensorByID := make(map[uint]model.Sensor, len(sensors))
	for _, sensor := range sensors {
		sensorByID[sensor.ID] = sensor
	}
	entriesBySensor := make(map[uint][]model.MeasurementEntry, len(entries))
	for _, entry := range entries {
		entriesBySensor[entry.SensorID] = append(entriesBySensor[entry.SensorID], entry)
	}
	issues := []model.BatchIssue{}
	for _, sensor := range sensors {
		if len(entriesBySensor[sensor.ID]) == 0 {
			issues = append(issues, model.BatchIssue{SensorID: sensor.ID, SensorName: sensor.Name, Type: constants.IssueMissing, Message: fmt.Sprintf("传感器「%s」缺项未录入", sensor.Name)})
		}
	}
	for sensorID, list := range entriesBySensor {
		sensor, ok := sensorByID[sensorID]
		if !ok {
			issues = append(issues, model.BatchIssue{SensorID: sensorID, SensorName: fmt.Sprintf("#%d", sensorID), Type: constants.IssueForeign, Message: "录入的传感器不属于该温室"})
			continue
		}
		if len(list) > 1 {
			issues = append(issues, model.BatchIssue{SensorID: sensorID, SensorName: sensor.Name, Type: constants.IssueDuplicate, Message: fmt.Sprintf("传感器「%s」在本批次中存在 %d 条录入", sensor.Name, len(list))})
		}
		for _, entry := range list {
			if entry.Value < sensor.Threshold.MinValue || entry.Value > sensor.Threshold.MaxValue {
				issues = append(issues, model.BatchIssue{SensorID: sensorID, SensorName: sensor.Name, Type: constants.IssueOutOfRange, Message: fmt.Sprintf("「%s」录入值 %.2f%s 超出阈值 [%.2f, %.2f]", sensor.Name, entry.Value, sensor.Unit, sensor.Threshold.MinValue, sensor.Threshold.MaxValue)})
			}
		}
	}
	return issues
}
