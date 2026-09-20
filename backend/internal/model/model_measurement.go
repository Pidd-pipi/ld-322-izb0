package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// BatchStatus 复核批次生命周期：草稿可反复录入修正，归档后不可变更。
const (
	BatchStatusDraft    = "draft"
	BatchStatusArchived = "archived"
)

// IssueCode 待修正项的原因类别。
const (
	IssueMissing   = "missing"   // 该温室传感器缺项
	IssueDuplicate = "duplicate" // 同一传感器在一批中出现多条
	IssueThreshold = "threshold" // 数值超出传感器阈值
)

// MeasurementBatch 温室测量复核批次。批次一旦创建，只允许录入其所属温室的传感器。
type MeasurementBatch struct {
	ID            uint         `gorm:"primaryKey" json:"id"`
	BatchNo       string       `gorm:"type:varchar(40);uniqueIndex;not null" json:"batchNo"`
	GreenhouseID  uint         `gorm:"index;not null" json:"greenhouseId"`
	Status        string       `gorm:"type:varchar(16);index;not null;default:draft" json:"status"`
	SubmittedAt   *time.Time   `json:"submittedAt,omitempty"`
	LastCheckedAt *time.Time   `json:"lastCheckedAt,omitempty"`
	LastIssues    BatchIssues  `gorm:"type:json" json:"lastIssues"`
	CreatedAt     time.Time    `json:"createdAt"`
	UpdatedAt     time.Time    `json:"updatedAt"`
	Greenhouse    *Greenhouse  `gorm:"foreignKey:GreenhouseID" json:"greenhouse,omitempty"`
	Entries       []BatchEntry `gorm:"foreignKey:BatchID" json:"entries,omitempty"`
}

// BatchEntry 批次中的单条测量录入。(batch_id, sensor_id) 唯一索引保证
// 同一传感器在一批中仅保留一条，并发录入由数据库拒绝而非覆盖。
type BatchEntry struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	BatchID   uint      `gorm:"uniqueIndex:uniq_batch_sensor;not null" json:"batchId"`
	SensorID  uint      `gorm:"uniqueIndex:uniq_batch_sensor;not null" json:"sensorId"`
	Value     float64   `json:"value"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Sensor    *Sensor   `gorm:"foreignKey:SensorID" json:"sensor,omitempty"`
}

// BatchIssue 复核发现的待修正项。
type BatchIssue struct {
	SensorID   uint    `json:"sensorId"`
	SensorName string  `json:"sensorName"`
	Type       string  `json:"type"`
	Code       string  `json:"code"`
	Message    string  `json:"message"`
	Value      float64 `json:"value,omitempty"`
	MinValue   float64 `json:"minValue,omitempty"`
	MaxValue   float64 `json:"maxValue,omitempty"`
}

// BatchIssues 支持以 JSON 列持久化的待修正项集合。
type BatchIssues []BatchIssue

// Scan 实现 sql.Scanner，从数据库 JSON 列还原。
func (b *BatchIssues) Scan(src any) error {
	if src == nil {
		*b = nil
		return nil
	}
	raw, ok := src.([]byte)
	if !ok {
		text, err := driver.String.ConvertValue(src)
		if err != nil {
			return fmt.Errorf("scan batch issues: %w", err)
		}
		raw = []byte(text.(string))
	}
	if len(raw) == 0 {
		*b = nil
		return nil
	}
	if err := json.Unmarshal(raw, b); err != nil {
		return fmt.Errorf("unmarshal batch issues: %w", err)
	}
	return nil
}

// Value 实现 driver.Valuer，序列化为 JSON 写入数据库。
func (b BatchIssues) Value() (driver.Value, error) {
	if b == nil {
		return "[]", nil
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("marshal batch issues: %w", err)
	}
	return string(raw), nil
}
