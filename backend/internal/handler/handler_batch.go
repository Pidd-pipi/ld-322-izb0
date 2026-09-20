package handler

import (
	"errors"
	"github.com/cygreenenv/greenhouse-panel/internal/dto"
	apperrors "github.com/cygreenenv/greenhouse-panel/internal/errors"
	"github.com/cygreenenv/greenhouse-panel/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"strconv"
)

type BatchHandler struct {
	service   *service.BatchService
	validator *validator.Validate
}

func NewBatchHandler(s *service.BatchService, v *validator.Validate) *BatchHandler {
	return &BatchHandler{s, v}
}

func (h *BatchHandler) Create(c *gin.Context) {
	greenhouseID, ok := parseID(c)
	if !ok {
		return
	}
	batch, err := h.service.Create(greenhouseID)
	if err != nil {
		Fail(c, err)
		return
	}
	Created(c, batch)
}

func (h *BatchHandler) List(c *gin.Context) {
	greenhouseID, ok := parseID(c)
	if !ok {
		return
	}
	rows, err := h.service.List(greenhouseID)
	if err != nil {
		Fail(c, err)
		return
	}
	Success(c, rows)
}

func (h *BatchHandler) Detail(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	batch, err := h.service.Detail(id)
	if err != nil {
		Fail(c, err)
		return
	}
	Success(c, batch)
}

func (h *BatchHandler) AddEntry(c *gin.Context) {
	batchID, ok := parseID(c)
	if !ok {
		return
	}
	var req dto.BatchEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil || h.validator.Struct(req) != nil {
		Fail(c, apperrors.ErrValidation)
		return
	}
	batch, err := h.service.AddEntry(batchID, req.SensorID, req.Value)
	if err != nil {
		Fail(c, err)
		return
	}
	Created(c, batch)
}

func (h *BatchHandler) UpdateEntry(c *gin.Context) {
	batchID, entryID, ok := parseEntryIDs(c)
	if !ok {
		return
	}
	var req dto.BatchEntryUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil || h.validator.Struct(req) != nil {
		Fail(c, apperrors.ErrValidation)
		return
	}
	batch, err := h.service.UpdateEntry(batchID, entryID, req.Value)
	if err != nil {
		Fail(c, err)
		return
	}
	Success(c, batch)
}

func (h *BatchHandler) DeleteEntry(c *gin.Context) {
	batchID, entryID, ok := parseEntryIDs(c)
	if !ok {
		return
	}
	batch, err := h.service.DeleteEntry(batchID, entryID)
	if err != nil {
		Fail(c, err)
		return
	}
	Success(c, batch)
}

// Submit 校验通过则归档；失败时返回 422 且 data 携带批次与待修正项。
func (h *BatchHandler) Submit(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	batch, err := h.service.Submit(id)
	if errors.Is(err, apperrors.ErrBatchRejected) {
		FailWithData(c, err, batch)
		return
	}
	if err != nil {
		Fail(c, err)
		return
	}
	Success(c, batch)
}

func parseEntryIDs(c *gin.Context) (uint, uint, bool) {
	batchID, ok := parseID(c)
	if !ok {
		return 0, 0, false
	}
	value, err := strconv.ParseUint(c.Param("entryId"), 10, 64)
	if err != nil || value == 0 {
		Fail(c, apperrors.ErrValidation)
		return 0, 0, false
	}
	return batchID, uint(value), true
}
