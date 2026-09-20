package dto

type BatchEntryRequest struct {
	SensorID uint    `json:"sensorId" validate:"required"`
	Value    float64 `json:"value"`
}
type BatchEntryUpdateRequest struct {
	Value float64 `json:"value"`
}
