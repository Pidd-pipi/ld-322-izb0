package handler

import (
	"github.com/cygreenenv/greenhouse-panel/internal/dto"
	apperrors "github.com/cygreenenv/greenhouse-panel/internal/errors"
	"github.com/cygreenenv/greenhouse-panel/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"net/http"
)

type MeasurementHandler struct {
	service   *service.MeasurementService
	validator *validator.Validate
}

func NewMeasurementHandler(s *service.MeasurementService, v *validator.Validate) *MeasurementHandler {
	return &MeasurementHandler{s, v}
}

// ListBatches GET /measurement/batches?greenhouse_id=&status=
func (h *MeasurementHandler) ListBatches(c *gin.Context) {
	var greenhouseID uint
	if raw := c.Query("greenhouse_id"); raw != "" {
		id, ok := queryID(c, "greenhouse_id")
		if !ok {
			return
		}
		greenhouseID = id
	}
	rows, err := h.service.ListBatches(greenhouseID, c.Query("status"))
	if err != nil {
		Fail(c, err)
		return
	}
	Success(c, rows)
}

// CreateBatch POST /measurement/batches
func (h *MeasurementHandler) CreateBatch(c *gin.Context) {
	var req dto.CreateBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil || h.validator.Struct(req) != nil {
		Fail(c, apperrors.ErrValidation)
		return
	}
	batch, err := h.service.CreateBatch(req.GreenhouseID)
	if err != nil {
		Fail(c, err)
		return
	}
	Created(c, batch)
}

// GetBatch GET /measurement/batches/:id
func (h *MeasurementHandler) GetBatch(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	batch, err := h.service.GetBatch(id)
	if err != nil {
		Fail(c, err)
		return
	}
	Success(c, batch)
}

// AddEntry POST /measurement/batches/:id/entries
func (h *MeasurementHandler) AddEntry(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req dto.BatchEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil || h.validator.Struct(req) != nil {
		Fail(c, apperrors.ErrValidation)
		return
	}
	entry, err := h.service.AddEntry(id, req.SensorID, req.Value)
	if err != nil {
		Fail(c, err)
		return
	}
	Created(c, entry)
}

// CorrectEntry PUT /measurement/batches/:id/entries — 修正既有录入（不允许靠重复录入覆盖）。
func (h *MeasurementHandler) CorrectEntry(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req dto.BatchEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil || h.validator.Struct(req) != nil {
		Fail(c, apperrors.ErrValidation)
		return
	}
	entry, err := h.service.CorrectEntry(id, req.SensorID, req.Value)
	if err != nil {
		Fail(c, err)
		return
	}
	Success(c, entry)
}

// Check GET /measurement/batches/:id/check — 返回当前待修正项（不改变状态）。
func (h *MeasurementHandler) Check(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	issues, err := h.service.Check(id)
	if err != nil {
		Fail(c, err)
		return
	}
	Success(c, gin.H{"batchId": id, "issues": issues, "passed": len(issues) == 0})
}

// Submit POST /measurement/batches/:id/submit — 整批校验；不合格返回 422 与待修正项。
func (h *MeasurementHandler) Submit(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	readings, batch, err := h.service.Submit(id)
	if err != nil {
		Fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "批次已归档", "data": gin.H{"batch": batch, "readings": readings}})
}
