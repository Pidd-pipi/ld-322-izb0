package model

import "time"

// MeasurementBatch 一次温室测量复核批次：先逐项录入草稿，再整批校验归档。
type MeasurementBatch struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	GreenhouseID uint   `gorm:"index" json:"greenhouseId"`
	Status       string `gorm:"type:varchar(20);index" json:"status"`
	// ActiveGreenhouseID 仅在批次未归档时等于温室 ID，配合唯一索引保证同一温室同一时刻只有一个未归档批次；归档后置 NULL。
	ActiveGreenhouseID *uint              `gorm:"uniqueIndex" json:"-"`
	IssuesJSON         string             `gorm:"column:issues;type:text" json:"-"`
	Issues             []BatchIssue       `gorm:"-" json:"issues,omitempty"`
	SubmittedAt        *time.Time         `json:"submittedAt,omitempty"`
	ArchivedAt         *time.Time         `json:"archivedAt,omitempty"`
	CreatedAt          time.Time          `json:"createdAt"`
	UpdatedAt          time.Time          `json:"updatedAt"`
	Entries            []MeasurementEntry `gorm:"foreignKey:BatchID" json:"entries,omitempty"`
}

// MeasurementEntry 批次内某一传感器的草稿读数；(batch_id, sensor_id) 唯一索引保证一批一传感器仅一条。
type MeasurementEntry struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	BatchID   uint      `gorm:"uniqueIndex:idx_batch_sensor" json:"batchId"`
	SensorID  uint      `gorm:"uniqueIndex:idx_batch_sensor" json:"sensorId"`
	Value     float64   `json:"value"`
	CreatedAt time.Time `json:"createdAt"`
	Sensor    Sensor    `json:"sensor,omitempty"`
}

// BatchIssue 提交校验失败时返回的待修正项。
type BatchIssue struct {
	SensorID   uint   `json:"sensorId"`
	SensorName string `json:"sensorName"`
	Type       string `json:"type"`
	Message    string `json:"message"`
}
