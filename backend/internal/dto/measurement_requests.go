package dto

// CreateBatchRequest 为指定温室创建测量复核批次。
type CreateBatchRequest struct {
	GreenhouseID uint `json:"greenhouseId" validate:"required"`
}

// BatchEntryRequest 向批次录入某个传感器的测量值。
type BatchEntryRequest struct {
	SensorID uint    `json:"sensorId" validate:"required"`
	Value    float64 `json:"value" validate:"required"`
}
