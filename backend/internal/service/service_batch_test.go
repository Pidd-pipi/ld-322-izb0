package service

import (
	"errors"
	apperrors "github.com/cygreenenv/greenhouse-panel/internal/errors"
	"github.com/cygreenenv/greenhouse-panel/internal/model"
	"github.com/cygreenenv/greenhouse-panel/internal/repository"
	ws "github.com/cygreenenv/greenhouse-panel/internal/websocket"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"io"
	"log/slog"
	"sync"
	"testing"
)

type batchFixture struct {
	db      *gorm.DB
	service *BatchService
	sensors []model.Sensor
	other   model.Sensor
}

func newBatchFixture(t *testing.T) *batchFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// sqlite 内存库不支持并发写事务，串行化连接以聚焦唯一约束与状态机语义；MySQL 下由行锁保证。
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err = db.AutoMigrate(model.All()...); err != nil {
		t.Fatal(err)
	}
	greenhouse := model.Greenhouse{Name: "复核温室" + t.Name()}
	if err = db.Create(&greenhouse).Error; err != nil {
		t.Fatal(err)
	}
	fixture := &batchFixture{db: db}
	ranges := []struct {
		name, unit string
		min, max   float64
	}{
		{"温度传感器", "°C", 10, 30},
		{"湿度传感器", "%", 40, 80},
	}
	for _, item := range ranges {
		sensor := model.Sensor{GreenhouseID: greenhouse.ID, Name: item.name, Type: "temperature", Unit: item.unit, Status: "online"}
		if err = db.Create(&sensor).Error; err != nil {
			t.Fatal(err)
		}
		if err = db.Create(&model.Threshold{SensorID: sensor.ID, MinValue: item.min, MaxValue: item.max}).Error; err != nil {
			t.Fatal(err)
		}
		fixture.sensors = append(fixture.sensors, sensor)
	}
	otherHouse := model.Greenhouse{Name: "其他温室" + t.Name()}
	if err = db.Create(&otherHouse).Error; err != nil {
		t.Fatal(err)
	}
	fixture.other = model.Sensor{GreenhouseID: otherHouse.ID, Name: "外部传感器", Type: "temperature", Unit: "°C", Status: "online"}
	if err = db.Create(&fixture.other).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.Threshold{SensorID: fixture.other.ID, MinValue: 0, MaxValue: 50}).Error; err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fixture.service = NewBatchService(repository.NewBatchRepository(db), repository.NewSensorRepository(db), repository.NewGreenhouseRepository(db), logger, ws.NewHub())
	return fixture
}

func (f *batchFixture) createBatch(t *testing.T, greenhouseID uint) *model.MeasurementBatch {
	t.Helper()
	batch, err := f.service.Create(greenhouseID)
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func (f *batchFixture) readingCount(t *testing.T) int64 {
	t.Helper()
	var count int64
	if err := f.db.Model(&model.SensorReading{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func TestBatchCreateRejectsSecondOpenBatch(t *testing.T) {
	f := newBatchFixture(t)
	batch := f.createBatch(t, f.sensors[0].GreenhouseID)
	if batch.Status != "pending" {
		t.Fatalf("want pending, got %s", batch.Status)
	}
	if _, err := f.service.Create(f.sensors[0].GreenhouseID); !errors.Is(err, apperrors.ErrBatchOpen) {
		t.Fatalf("want ErrBatchOpen, got %v", err)
	}
}

func TestBatchAddEntryRejectsForeignSensor(t *testing.T) {
	f := newBatchFixture(t)
	batch := f.createBatch(t, f.sensors[0].GreenhouseID)
	if _, err := f.service.AddEntry(batch.ID, f.other.ID, 20); !errors.Is(err, apperrors.ErrSensorMismatch) {
		t.Fatalf("want ErrSensorMismatch, got %v", err)
	}
}

func TestBatchAddEntryDuplicateNeverOverwrites(t *testing.T) {
	f := newBatchFixture(t)
	batch := f.createBatch(t, f.sensors[0].GreenhouseID)
	if _, err := f.service.AddEntry(batch.ID, f.sensors[0].ID, 21.5); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.AddEntry(batch.ID, f.sensors[0].ID, 99); !errors.Is(err, apperrors.ErrEntryDuplicate) {
		t.Fatalf("want ErrEntryDuplicate, got %v", err)
	}
	detail, err := f.service.Detail(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Entries) != 1 || detail.Entries[0].Value != 21.5 {
		t.Fatalf("duplicate entry overwrote data: %+v", detail.Entries)
	}
}

func TestBatchConcurrentAddEntryKeepsSingleRow(t *testing.T) {
	f := newBatchFixture(t)
	batch := f.createBatch(t, f.sensors[0].GreenhouseID)
	var wait sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func(value float64) {
			defer wait.Done()
			_, err := f.service.AddEntry(batch.ID, f.sensors[0].ID, value)
			results <- err
		}(float64(15 + i))
	}
	wait.Wait()
	close(results)
	succeeded, duplicates := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, apperrors.ErrEntryDuplicate) {
			duplicates++
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 || duplicates != 7 {
		t.Fatalf("want 1 success + 7 duplicates, got %d/%d", succeeded, duplicates)
	}
	detail, err := f.service.Detail(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Entries) != 1 {
		t.Fatalf("want exactly 1 entry, got %d", len(detail.Entries))
	}
}

func TestBatchSubmitRejectsMissingAndOutOfRange(t *testing.T) {
	f := newBatchFixture(t)
	batch := f.createBatch(t, f.sensors[0].GreenhouseID)
	// 只录入一个传感器且越限，另一个缺项
	if _, err := f.service.AddEntry(batch.ID, f.sensors[0].ID, 55); err != nil {
		t.Fatal(err)
	}
	rejected, err := f.service.Submit(batch.ID)
	if !errors.Is(err, apperrors.ErrBatchRejected) {
		t.Fatalf("want ErrBatchRejected, got %v", err)
	}
	if rejected.Status != "rejected" {
		t.Fatalf("want rejected status, got %s", rejected.Status)
	}
	if len(rejected.Issues) != 2 {
		t.Fatalf("want 2 issues (missing + out_of_range), got %d: %+v", len(rejected.Issues), rejected.Issues)
	}
	types := map[string]bool{}
	for _, issue := range rejected.Issues {
		types[issue.Type] = true
	}
	if !types["missing"] || !types["out_of_range"] {
		t.Fatalf("unexpected issue types: %+v", rejected.Issues)
	}
	if count := f.readingCount(t); count != 0 {
		t.Fatalf("rejected batch must not create readings, got %d", count)
	}
	// 刷新后（重新查询）状态与待修正项保持一致
	reloaded, err := f.service.Detail(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != "rejected" || len(reloaded.Issues) != 2 {
		t.Fatalf("reloaded batch mismatch: %+v", reloaded)
	}
}

func TestBatchFixAndResubmitArchivesAtomically(t *testing.T) {
	f := newBatchFixture(t)
	batch := f.createBatch(t, f.sensors[0].GreenhouseID)
	if _, err := f.service.AddEntry(batch.ID, f.sensors[0].ID, 55); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Submit(batch.ID); !errors.Is(err, apperrors.ErrBatchRejected) {
		t.Fatalf("want ErrBatchRejected, got %v", err)
	}
	// 修正越限值并补齐缺项
	detail, err := f.service.Detail(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.UpdateEntry(batch.ID, detail.Entries[0].ID, 22); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.AddEntry(batch.ID, f.sensors[1].ID, 60); err != nil {
		t.Fatal(err)
	}
	archived, err := f.service.Submit(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Status != "archived" || archived.ArchivedAt == nil {
		t.Fatalf("want archived batch, got %+v", archived)
	}
	if count := f.readingCount(t); count != 2 {
		t.Fatalf("want 2 archived readings, got %d", count)
	}
	// 归档后追加、修正、删除、重复提交均直接失败
	if _, err = f.service.AddEntry(batch.ID, f.sensors[0].ID, 20); !errors.Is(err, apperrors.ErrBatchArchived) {
		t.Fatalf("append after archive: want ErrBatchArchived, got %v", err)
	}
	if _, err = f.service.UpdateEntry(batch.ID, detail.Entries[0].ID, 20); !errors.Is(err, apperrors.ErrBatchArchived) {
		t.Fatalf("update after archive: want ErrBatchArchived, got %v", err)
	}
	if _, err = f.service.DeleteEntry(batch.ID, detail.Entries[0].ID); !errors.Is(err, apperrors.ErrBatchArchived) {
		t.Fatalf("delete after archive: want ErrBatchArchived, got %v", err)
	}
	if _, err = f.service.Submit(batch.ID); !errors.Is(err, apperrors.ErrBatchArchived) {
		t.Fatalf("resubmit after archive: want ErrBatchArchived, got %v", err)
	}
	if count := f.readingCount(t); count != 2 {
		t.Fatalf("readings must stay 2 after failed operations, got %d", count)
	}
	// 归档后允许开启新批次
	if _, err = f.service.Create(f.sensors[0].GreenhouseID); err != nil {
		t.Fatalf("new batch after archive should succeed, got %v", err)
	}
}

func TestBatchConcurrentSubmitArchivesOnce(t *testing.T) {
	f := newBatchFixture(t)
	batch := f.createBatch(t, f.sensors[0].GreenhouseID)
	for _, sensor := range f.sensors {
		value := 20.0
		if sensor.Unit == "%" {
			value = 60
		}
		if _, err := f.service.AddEntry(batch.ID, sensor.ID, value); err != nil {
			t.Fatal(err)
		}
	}
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := f.service.Submit(batch.ID)
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	archived, conflicts := 0, 0
	for err := range results {
		if err == nil {
			archived++
		} else if errors.Is(err, apperrors.ErrBatchArchived) {
			conflicts++
		} else {
			t.Fatalf("unexpected submit error: %v", err)
		}
	}
	if archived != 1 || conflicts != 1 {
		t.Fatalf("want 1 archive + 1 conflict, got %d/%d", archived, conflicts)
	}
	if count := f.readingCount(t); count != 2 {
		t.Fatalf("concurrent submit must archive exactly once, got %d readings", count)
	}
}
