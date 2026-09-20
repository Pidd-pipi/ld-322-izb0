package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/cygreenenv/greenhouse-panel/internal/model"
	"github.com/cygreenenv/greenhouse-panel/internal/repository"
	"github.com/cygreenenv/greenhouse-panel/internal/service"
	ws "github.com/cygreenenv/greenhouse-panel/internal/websocket"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type batchTestEnv struct {
	engine  *gin.Engine
	db      *gorm.DB
	token   string
	sensors []model.Sensor
	foreign model.Sensor
}

type apiEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func newBatchTestEnv(t *testing.T) *batchTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:router_batch_test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err = db.AutoMigrate(model.All()...); err != nil {
		t.Fatal(err)
	}
	env := &batchTestEnv{db: db}
	greenhouse := model.Greenhouse{Name: "路由测试温室", Location: "测试区", Area: 100}
	if err = db.Create(&greenhouse).Error; err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name, unit string
		min, max   float64
	}{{"温度传感器", "°C", 10, 30}, {"湿度传感器", "%", 40, 80}} {
		sensor := model.Sensor{GreenhouseID: greenhouse.ID, Name: item.name, Type: "temperature", Unit: item.unit, Status: "online"}
		if err = db.Create(&sensor).Error; err != nil {
			t.Fatal(err)
		}
		if err = db.Create(&model.Threshold{SensorID: sensor.ID, MinValue: item.min, MaxValue: item.max}).Error; err != nil {
			t.Fatal(err)
		}
		env.sensors = append(env.sensors, sensor)
	}
	other := model.Greenhouse{Name: "路由测试外部温室", Location: "外部", Area: 50}
	if err = db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	env.foreign = model.Sensor{GreenhouseID: other.ID, Name: "外部传感器", Type: "temperature", Unit: "°C", Status: "online"}
	if err = db.Create(&env.foreign).Error; err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auth := service.NewAuthService("router-test-secret")
	greenhouses := repository.NewGreenhouseRepository(db)
	sensors := repository.NewSensorRepository(db)
	batches := service.NewBatchService(repository.NewBatchRepository(db), sensors, greenhouses, logger, ws.NewHub())
	env.engine = New(Dependencies{Logger: logger, Auth: auth, Batches: batches, Hub: ws.NewHub()})
	if env.token, err = auth.Login("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	return env
}

func (e *batchTestEnv) call(t *testing.T, method, path, body string, withAuth bool) (int, apiEnvelope) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if withAuth {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	recorder := httptest.NewRecorder()
	e.engine.ServeHTTP(recorder, req)
	var envelope apiEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response %s %s: %v (%s)", method, path, err, recorder.Body.String())
	}
	return recorder.Code, envelope
}

func (e *batchTestEnv) readingCount(t *testing.T) int64 {
	t.Helper()
	var count int64
	if err := e.db.Model(&model.SensorReading{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func TestBatchClosedLoopHTTP(t *testing.T) {
	env := newBatchTestEnv(t)
	greenhouseID := env.sensors[0].GreenhouseID
	base := fmt.Sprintf("/api/v1/greenhouses/%d/batches", greenhouseID)

	// 未认证写操作被拒绝
	if status, _ := env.call(t, http.MethodPost, base, "", false); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create: want 401, got %d", status)
	}
	// 创建批次
	status, created := env.call(t, http.MethodPost, base, "", true)
	if status != http.StatusCreated {
		t.Fatalf("create batch: want 201, got %d", status)
	}
	var batch model.MeasurementBatch
	if err := json.Unmarshal(created.Data, &batch); err != nil {
		t.Fatal(err)
	}
	if batch.Status != "pending" {
		t.Fatalf("want pending batch, got %s", batch.Status)
	}
	// 重复创建开放批次 → 409
	if status, _ = env.call(t, http.MethodPost, base, "", true); status != http.StatusConflict {
		t.Fatalf("second open batch: want 409, got %d", status)
	}
	entries := fmt.Sprintf("/api/v1/batches/%d/entries", batch.ID)
	// 非本温室传感器 → 400
	if status, _ = env.call(t, http.MethodPost, entries, fmt.Sprintf(`{"sensorId":%d,"value":20}`, env.foreign.ID), true); status != http.StatusBadRequest {
		t.Fatalf("foreign sensor entry: want 400, got %d", status)
	}
	// 正常录入 + 重复录入 → 409 且不覆盖
	if status, _ = env.call(t, http.MethodPost, entries, fmt.Sprintf(`{"sensorId":%d,"value":25}`, env.sensors[0].ID), true); status != http.StatusCreated {
		t.Fatalf("add entry: want 201, got %d", status)
	}
	if status, _ = env.call(t, http.MethodPost, entries, fmt.Sprintf(`{"sensorId":%d,"value":99}`, env.sensors[0].ID), true); status != http.StatusConflict {
		t.Fatalf("duplicate entry: want 409, got %d", status)
	}
	status, detail := env.call(t, http.MethodGet, fmt.Sprintf("/api/v1/batches/%d", batch.ID), "", true)
	if status != http.StatusOK {
		t.Fatalf("get batch: want 200, got %d", status)
	}
	var current model.MeasurementBatch
	if err := json.Unmarshal(detail.Data, &current); err != nil {
		t.Fatal(err)
	}
	if len(current.Entries) != 1 || current.Entries[0].Value != 25 {
		t.Fatalf("duplicate entry overwrote value: %+v", current.Entries)
	}
	// 缺项提交 → 422，返回待修正项，不产生正式读数
	status, rejected := env.call(t, http.MethodPost, fmt.Sprintf("/api/v1/batches/%d/submit", batch.ID), "", true)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("submit with missing: want 422, got %d", status)
	}
	var rejectedBatch model.MeasurementBatch
	if err := json.Unmarshal(rejected.Data, &rejectedBatch); err != nil {
		t.Fatal(err)
	}
	if rejectedBatch.Status != "rejected" || len(rejectedBatch.Issues) != 1 || rejectedBatch.Issues[0].Type != "missing" {
		t.Fatalf("want 1 missing issue, got %+v", rejectedBatch.Issues)
	}
	if count := env.readingCount(t); count != 0 {
		t.Fatalf("rejected submit created readings: %d", count)
	}
	// 补录但越限 → 仍 422
	if status, _ = env.call(t, http.MethodPost, entries, fmt.Sprintf(`{"sensorId":%d,"value":120}`, env.sensors[1].ID), true); status != http.StatusCreated {
		t.Fatalf("add second entry: want 201, got %d", status)
	}
	status, rejected = env.call(t, http.MethodPost, fmt.Sprintf("/api/v1/batches/%d/submit", batch.ID), "", true)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("submit out of range: want 422, got %d", status)
	}
	if err := json.Unmarshal(rejected.Data, &rejectedBatch); err != nil {
		t.Fatal(err)
	}
	if len(rejectedBatch.Issues) != 1 || rejectedBatch.Issues[0].Type != "out_of_range" {
		t.Fatalf("want out_of_range issue, got %+v", rejectedBatch.Issues)
	}
	// 修正越限值后提交 → 归档并写入正式读数
	entryID := rejectedBatch.Entries[len(rejectedBatch.Entries)-1].ID
	if status, _ = env.call(t, http.MethodPut, fmt.Sprintf("/api/v1/batches/%d/entries/%d", batch.ID, entryID), `{"value":60}`, true); status != http.StatusOK {
		t.Fatalf("fix entry: want 200, got %d", status)
	}
	status, archived := env.call(t, http.MethodPost, fmt.Sprintf("/api/v1/batches/%d/submit", batch.ID), "", true)
	if status != http.StatusOK {
		t.Fatalf("submit valid batch: want 200, got %d", status)
	}
	var archivedBatch model.MeasurementBatch
	if err := json.Unmarshal(archived.Data, &archivedBatch); err != nil {
		t.Fatal(err)
	}
	if archivedBatch.Status != "archived" || archivedBatch.ArchivedAt == nil {
		t.Fatalf("want archived batch, got %+v", archivedBatch)
	}
	if count := env.readingCount(t); count != 2 {
		t.Fatalf("want 2 archived readings, got %d", count)
	}
	// 归档后追加与重复提交 → 409
	if status, _ = env.call(t, http.MethodPost, entries, fmt.Sprintf(`{"sensorId":%d,"value":20}`, env.sensors[0].ID), true); status != http.StatusConflict {
		t.Fatalf("append after archive: want 409, got %d", status)
	}
	if status, _ = env.call(t, http.MethodPost, fmt.Sprintf("/api/v1/batches/%d/submit", batch.ID), "", true); status != http.StatusConflict {
		t.Fatalf("resubmit after archive: want 409, got %d", status)
	}
	// 列表接口刷新后状态一致
	status, list := env.call(t, http.MethodGet, base, "", true)
	if status != http.StatusOK {
		t.Fatalf("list batches: want 200, got %d", status)
	}
	var batches []model.MeasurementBatch
	if err := json.Unmarshal(list.Data, &batches); err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || batches[0].Status != "archived" || len(batches[0].Entries) != 2 {
		t.Fatalf("list after archive mismatch: %+v", batches)
	}
	// 归档后可开启新批次
	if status, _ = env.call(t, http.MethodPost, base, "", true); status != http.StatusCreated {
		t.Fatalf("new batch after archive: want 201, got %d", status)
	}
}
