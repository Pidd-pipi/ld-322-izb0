package router_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cygreenenv/greenhouse-panel/internal/model"
	"github.com/cygreenenv/greenhouse-panel/internal/repository"
	"github.com/cygreenenv/greenhouse-panel/internal/router"
	"github.com/cygreenenv/greenhouse-panel/internal/service"
	ws "github.com/cygreenenv/greenhouse-panel/internal/websocket"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func setupMeasurementServer(t *testing.T) (*httptest.Server, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:measurement_http_test?mode=memory&cache=shared&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err = db.AutoMigrate(model.All()...); err != nil {
		t.Fatal(err)
	}
	g := model.Greenhouse{Name: "HTTP 复核温室", Location: "测试区", Area: 100}
	if err = db.Create(&g).Error; err != nil {
		t.Fatal(err)
	}
	type spec struct {
		typ, name, unit string
		min, max        float64
	}
	for _, sp := range []spec{
		{"temperature", "温度", "°C", 15, 32},
		{"humidity", "湿度", "%", 45, 80},
	} {
		s := model.Sensor{GreenhouseID: g.ID, Name: sp.name, Type: sp.typ, Unit: sp.unit, Status: "online"}
		if err = db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
		if err = db.Create(&model.Threshold{SensorID: s.ID, MinValue: sp.min, MaxValue: sp.max}).Error; err != nil {
			t.Fatal(err)
		}
	}
	other := model.Greenhouse{Name: "HTTP 另一温室", Location: "别处", Area: 50}
	if err = db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	otherSensor := model.Sensor{GreenhouseID: other.ID, Name: "温度", Type: "temperature", Unit: "°C", Status: "online"}
	if err = db.Create(&otherSensor).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.Threshold{SensorID: otherSensor.ID, MinValue: 0, MaxValue: 100}).Error; err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	greenRepo := repository.NewGreenhouseRepository(db)
	sensorRepo := repository.NewSensorRepository(db)
	alertRepo := repository.NewAlertRepository(db)
	deviceRepo := repository.NewDeviceRepository(db)
	batchRepo := repository.NewMeasurementRepository(db)
	hub := ws.NewHub()
	authSvc := service.NewAuthService("test-secret")
	monitoring := service.NewMonitoringService(greenRepo, sensorRepo, alertRepo, logger, hub)
	control := service.NewControlService(deviceRepo, logger, hub)
	alerts := service.NewAlertService(alertRepo, logger)
	reports := service.NewReportService(sensorRepo, alertRepo)
	measurement := service.NewMeasurementService(batchRepo, greenRepo, sensorRepo, logger, hub)
	engine := router.New(router.Dependencies{Logger: logger, Auth: authSvc, Monitoring: monitoring, Alerts: alerts, Control: control, Reports: reports, Measurement: measurement, Hub: hub})
	return httptest.NewServer(engine), db
}

func doJSON(t *testing.T, method, url string, body any, token string) (int, envelope) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env envelope
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &env)
	}
	return resp.StatusCode, env
}

func TestMeasurementClosedLoopOverHTTP(t *testing.T) {
	server, db := setupMeasurementServer(t)
	defer server.Close()

	// 登录拿令牌（与前端演示登录一致）。
	status, env := doJSON(t, http.MethodPost, server.URL+"/api/v1/auth/login", map[string]string{"username": "admin", "password": "admin123"}, "")
	if status != http.StatusOK {
		t.Fatalf("login status=%d message=%s", status, env.Message)
	}
	var auth struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(env.Data, &auth)
	if auth.Token == "" {
		t.Fatal("missing token")
	}
	token := auth.Token

	// 写操作需要鉴权。
	if status, _ = doJSON(t, http.MethodPost, server.URL+"/api/v1/measurement/batches", map[string]uint{"greenhouseId": 1}, ""); status != http.StatusUnauthorized {
		t.Fatalf("want 401 without token, got %d", status)
	}

	// 1) 创建批次。
	status, env = doJSON(t, http.MethodPost, server.URL+"/api/v1/measurement/batches", map[string]uint{"greenhouseId": 1}, token)
	if status != http.StatusCreated {
		t.Fatalf("create batch status=%d message=%s", status, env.Message)
	}
	var batch model.MeasurementBatch
	_ = json.Unmarshal(env.Data, &batch)
	if batch.Status != "draft" {
		t.Fatalf("want draft, got %s", batch.Status)
	}

	// 2) 录入别的温室的传感器：403。
	status, _ = doJSON(t, http.MethodPost, server.URL+"/api/v1/measurement/batches/1/entries", map[string]any{"sensorId": 3, "value": 20}, token)
	if status != http.StatusForbidden {
		t.Fatalf("want 403 for foreign sensor, got %d", status)
	}

	// 3) 先录一个超阈值湿度，一个合格温度。
	if status, _ = doJSON(t, http.MethodPost, server.URL+"/api/v1/measurement/batches/1/entries", map[string]any{"sensorId": 1, "value": 24}, token); status != http.StatusCreated {
		t.Fatalf("entry temp status=%d", status)
	}
	if status, _ = doJSON(t, http.MethodPost, server.URL+"/api/v1/measurement/batches/1/entries", map[string]any{"sensorId": 2, "value": 99}, token); status != http.StatusCreated {
		t.Fatalf("entry humidity status=%d", status)
	}

	// 4) 重复录入同一传感器（即便带新值）：409 且不覆盖。
	if status, env = doJSON(t, http.MethodPost, server.URL+"/api/v1/measurement/batches/1/entries", map[string]any{"sensorId": 1, "value": 30}, token); status != http.StatusConflict {
		t.Fatalf("want 409 duplicate, got %d", status)
	}

	// 5) 提交：湿度超阈值 → 422 整批拒绝，读数表为空。
	status, env = doJSON(t, http.MethodPost, server.URL+"/api/v1/measurement/batches/1/submit", nil, token)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", status)
	}
	var rejection struct {
		BatchID uint               `json:"batchId"`
		Issues  []model.BatchIssue `json:"issues"`
	}
	_ = json.Unmarshal(env.Data, &rejection)
	if len(rejection.Issues) != 1 || rejection.Issues[0].Code != model.IssueThreshold {
		t.Fatalf("want threshold issue, got %#v", rejection.Issues)
	}
	var readings []model.SensorReading
	if err := db.Find(&readings).Error; err != nil || len(readings) != 0 {
		t.Fatalf("want zero formal readings, got %d err=%v", len(readings), err)
	}

	// 6) 刷新后仍能看到失败原因（持久化在批次上）。
	status, env = doJSON(t, http.MethodGet, server.URL+"/api/v1/measurement/batches/1", nil, token)
	if status != http.StatusOK {
		t.Fatalf("get batch status=%d", status)
	}
	var refreshed model.MeasurementBatch
	_ = json.Unmarshal(env.Data, &refreshed)
	if len(refreshed.LastIssues) != 1 || refreshed.Status != "draft" {
		t.Fatalf("issues should survive refresh, got %#v", refreshed)
	}

	// 7) PUT 显式修正湿度为合格值。
	if status, _ = doJSON(t, http.MethodPut, server.URL+"/api/v1/measurement/batches/1/entries", map[string]any{"sensorId": 2, "value": 60}, token); status != http.StatusOK {
		t.Fatalf("correct entry status=%d", status)
	}

	// 8) 再次提交：200 归档并写入两条读数。
	status, env = doJSON(t, http.MethodPost, server.URL+"/api/v1/measurement/batches/1/submit", nil, token)
	if status != http.StatusOK {
		t.Fatalf("want 200 archive, got %d", status)
	}
	if err := db.Find(&readings).Error; err != nil || len(readings) != 2 {
		t.Fatalf("want 2 formal readings, got %d err=%v", len(readings), err)
	}

	// 9) 归档后追加录入与重复提交：409。
	if status, _ = doJSON(t, http.MethodPost, server.URL+"/api/v1/measurement/batches/1/entries", map[string]any{"sensorId": 1, "value": 21}, token); status != http.StatusConflict {
		t.Fatalf("want 409 append after archive, got %d", status)
	}
	if status, _ = doJSON(t, http.MethodPost, server.URL+"/api/v1/measurement/batches/1/submit", nil, token); status != http.StatusConflict {
		t.Fatalf("want 409 resubmit, got %d", status)
	}

	// 10) 列表接口能展示已归档状态。
	status, env = doJSON(t, http.MethodGet, server.URL+"/api/v1/measurement/batches?greenhouse_id=1", nil, token)
	if status != http.StatusOK {
		t.Fatalf("list status=%d", status)
	}
	var list []model.MeasurementBatch
	_ = json.Unmarshal(env.Data, &list)
	if len(list) != 1 || list[0].Status != model.BatchStatusArchived {
		t.Fatalf("want one archived batch in list, got %#v", list)
	}
}

func init() {
	// HTTP 默认客户端需要超时，避免测试挂死。
	http.DefaultClient.Timeout = 5 * time.Second
}
